package payments

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Service) authorized(w http.ResponseWriter, r *http.Request) bool {
	values := r.Header.Values("X-Internal-Key")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(s.config.InternalAPIKey)) != 1 {
		apiError(w, 401, "unauthorized", "La operación requiere credenciales internas válidas.")
		return false
	}
	return true
}
func (s *Service) chargeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		apiError(w, 400, "invalid_request", "Envía una solicitud JSON válida.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input ChargeInput
	if err := decoder.Decode(&input); err != nil {
		apiError(w, 400, "invalid_request", "Revisa los datos del pago simulado.")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		apiError(w, 400, "invalid_request", "La solicitud debe contener un único objeto JSON.")
		return
	}
	transaction, err := s.Charge(input)
	// Drop request references promptly. No claim of secure memory erasure in the Go runtime.
	if input.Card != nil {
		input.Card.Number = ""
		input.Card.CVC = ""
	}
	switch {
	case errors.Is(err, ErrConflict):
		apiError(w, 409, "idempotency_conflict", "La orden ya fue procesada con otros datos de pago.")
	case errors.Is(err, ErrInvalid):
		apiError(w, 400, "invalid_request", "Revisa el importe, la moneda y los datos de la tarjeta ficticia.")
	case err != nil:
		apiError(w, 500, "internal_error", "No pudimos procesar el pago simulado.")
	default:
		writeJSON(w, 200, transaction)
	}
}
func (s *Service) lookupHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	transaction, err := s.ByOrder(r.PathValue("orderId"))
	if errors.Is(err, ErrNotFound) {
		apiError(w, 404, "not_found", "No encontramos una transacción para la orden.")
		return
	}
	if err != nil {
		apiError(w, 400, "invalid_request", "La orden no es válida.")
		return
	}
	writeJSON(w, 200, transaction)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (s *Service) Handler(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			apiError(w, 405, "invalid_request", "El método no está permitido.")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok", "service": "payments", "storage": "memory", "time": s.clock().UTC().Format(time.RFC3339Nano)})
	})
	mux.HandleFunc("/internal/payments/charge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			apiError(w, 405, "invalid_request", "El método no está permitido.")
			return
		}
		s.chargeHTTP(w, r)
	})
	mux.HandleFunc("/internal/payments/by-order/{orderId}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			apiError(w, 405, "invalid_request", "El método no está permitido.")
			return
		}
		s.lookupHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 404, "not_found", "No encontramos el recurso.")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		out := &statusWriter{ResponseWriter: w}
		defer func() {
			if recover() != nil {
				if out.status == 0 {
					apiError(out, 500, "internal_error", "No pudimos procesar la solicitud.")
				}
				if logger != nil {
					logger.Error("request_failed")
				}
			}
			if logger != nil {
				logger.Info("request_completed", "method", r.Method, "route", r.Pattern, "status", out.status, "elapsedMs", time.Since(started).Milliseconds())
			}
		}()
		mux.ServeHTTP(out, r)
	})
}
