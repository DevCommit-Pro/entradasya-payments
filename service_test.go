package payments

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

const testKey = "test-only-internal-key-with-at-least-32-bytes"

func service(t *testing.T) *Service {
	t.Helper()
	s, err := New(Config{Port: 4004, InternalAPIKey: testKey}, func() time.Time { return time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func input(id string) ChargeInput {
	amount := Money(17050)
	return ChargeInput{OrderID: id, Amount: &amount, Currency: "BOB", Card: &Card{Number: "4242424242424242", Holder: "Camila Rojas", Expiry: "08/28", CVC: "123"}}
}
func call(t *testing.T, s *Service, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Internal-Key", key)
	}
	out := httptest.NewRecorder()
	s.Handler(nil).ServeHTTP(out, req)
	return out
}
func body(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err, string(response.Body.Bytes()))
	}
	return value
}
func payload(t *testing.T, value ChargeInput) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestApprovedDeclinedAndLookup(t *testing.T) {
	s := service(t)
	for _, declined := range []bool{false, true} {
		in := input("approved")
		want := "approved"
		if declined {
			in.OrderID = "declined"
			in.Card.Number = "4000000000000000"
			want = "declined"
		}
		response := call(t, s, "POST", "/internal/payments/charge", testKey, payload(t, in))
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		value := body(t, response)
		if value["status"] != want || value["amount"] != 170.5 || value["currency"] != "BOB" {
			t.Fatal(value)
		}
		lookup := call(t, s, "GET", "/internal/payments/by-order/"+in.OrderID, testKey, "")
		if lookup.Code != 200 || body(t, lookup)["transactionId"] != value["transactionId"] {
			t.Fatal(lookup.Body.String())
		}
		if strings.Contains(response.Body.String(), in.Card.Number) || strings.Contains(response.Body.String(), "cvc") {
			t.Fatal("sensitive card data leaked")
		}
	}
}
func TestIdempotencyAndConflicts(t *testing.T) {
	s := service(t)
	in := input("same")
	first, err := s.Charge(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Charge(in)
	if err != nil || first != second {
		t.Fatal(first, second, err)
	}
	changed := input("same")
	changed.Card.Number = "4000000000000000"
	if _, err = s.Charge(changed); err != ErrConflict {
		t.Fatal(err)
	}
	changed = input("same")
	*changed.Amount += 1
	if _, err = s.Charge(changed); err != ErrConflict {
		t.Fatal(err)
	}
	if len(s.transactions) != 1 {
		t.Fatal("duplicate transaction")
	}
}
func TestConcurrentSameOrder(t *testing.T) {
	s := service(t)
	in := input("concurrent")
	var wg sync.WaitGroup
	ids := make(chan string, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Charge(in)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- result.TransactionID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 || len(s.transactions) != 1 {
		t.Fatal("duplicate charge", len(seen), len(s.transactions))
	}
}
func TestInvalidInputsDoNotRecordTransactions(t *testing.T) {
	s := service(t)
	for _, edit := range []func(*ChargeInput){func(i *ChargeInput) { i.OrderID = " " }, func(i *ChargeInput) { i.Amount = nil }, func(i *ChargeInput) { *i.Amount = -1 }, func(i *ChargeInput) { i.Currency = "USD" }, func(i *ChargeInput) { i.Card = nil }, func(i *ChargeInput) { i.Card.Number = "123" }, func(i *ChargeInput) { i.Card.CVC = "12" }, func(i *ChargeInput) { i.Card.Expiry = "13/28" }, func(i *ChargeInput) { i.Card.Holder = " " }} {
		in := input("invalid")
		edit(&in)
		if _, err := s.Charge(in); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	if len(s.transactions) != 0 {
		t.Fatal("invalid request mutated state")
	}
}
func TestHTTPErrorEnvelopesAndPrivateAccess(t *testing.T) {
	s := service(t)
	valid := payload(t, input("private"))
	for _, key := range []string{"", "incorrect"} {
		response := call(t, s, "POST", "/internal/payments/charge", key, valid)
		if response.Code != 401 || body(t, response)["error"].(map[string]any)["code"] != "unauthorized" {
			t.Fatal(response.Body.String())
		}
	}
	for _, raw := range []string{"{", "null", "[]", `{"amount":10}`, valid + valid, strings.Replace(valid, `170.50`, `"170.50"`, 1), strings.Replace(valid, `170.50`, `null`, 1), strings.Replace(valid, `170.50`, `0.001`, 1)} {
		response := call(t, s, "POST", "/internal/payments/charge", testKey, raw)
		if response.Code != 400 || body(t, response)["error"].(map[string]any)["code"] != "invalid_request" {
			t.Fatal(response.Code, raw, response.Body.String())
		}
	}
	response := call(t, s, "DELETE", "/internal/payments/charge", testKey, "")
	if response.Code != 405 || response.Header().Get("Allow") != "POST" {
		t.Fatal(response.Code)
	}
	response = call(t, s, "GET", "/unknown", "", "")
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
	response = call(t, s, "GET", "/internal/payments/by-order/missing", testKey, "")
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
}
func TestDuplicateInternalHeadersAndOversizedBody(t *testing.T) {
	s := service(t)
	req := httptest.NewRequest("POST", "/internal/payments/charge", strings.NewReader(payload(t, input("headers"))))
	req.Header.Add("X-Internal-Key", testKey)
	req.Header.Add("X-Internal-Key", testKey)
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	s.Handler(nil).ServeHTTP(out, req)
	if out.Code != 401 {
		t.Fatal(out.Code)
	}
	raw := `{"orderId":"large","extra":"` + strings.Repeat("a", 70*1024) + `"}`
	out = call(t, s, "POST", "/internal/payments/charge", testKey, raw)
	if out.Code != 400 {
		t.Fatal(out.Code)
	}
}
func TestExactMoney(t *testing.T) {
	for _, raw := range []string{"0", "0.01", "260.5", "1e2", "1.2300", "90071992547409.91"} {
		var value Money
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(raw, err)
		}
		roundtrip, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var restored Money
		if json.Unmarshal(roundtrip, &restored) != nil || restored != value {
			t.Fatal(raw, string(roundtrip))
		}
	}
	for _, raw := range []string{`"1"`, `null`, `true`, `-1`, `1.001`, `1.000000000000000000001`, `1e9999999`, `90071992547409.92`} {
		var value Money
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatal("accepted invalid amount", raw)
		}
	}
}
func TestNoPANBackingStringOrLogs(t *testing.T) {
	s := service(t)
	in := input("safe")
	transaction, err := s.Charge(in)
	if err != nil {
		t.Fatal(err)
	}
	if unsafe.StringData(transaction.CardLast4) == unsafe.StringData(in.Card.Number[len(in.Card.Number)-4:]) {
		t.Fatal("last4 retained full PAN backing storage")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	req := httptest.NewRequest("POST", "/internal/payments/charge", strings.NewReader(payload(t, input("logged"))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", testKey)
	s.Handler(logger).ServeHTTP(httptest.NewRecorder(), req)
	for _, secret := range []string{in.Card.Number, testKey, `"cvc"`, `"holder"`} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("sensitive log")
		}
	}
	if !strings.Contains(logs.String(), "request_completed") {
		t.Fatal("missing structured log")
	}
}
func TestConfigAndHealth(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("accepted invalid config")
	}
	s := service(t)
	response := call(t, s, "GET", "/health", "", "")
	if response.Code != http.StatusOK || body(t, response)["storage"] != "memory" {
		t.Fatal(response.Body.String())
	}
	t.Setenv("INTERNAL_API_KEY", testKey)
	t.Setenv("PORT", "4004")
	if _, err := ConfigFromEnv(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "0")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("invalid port")
	}
}
