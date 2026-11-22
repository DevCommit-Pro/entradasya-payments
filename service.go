package payments

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Card struct {
	Number string `json:"number"`
	Holder string `json:"holder"`
	Expiry string `json:"expiry"`
	CVC    string `json:"cvc"`
}
type ChargeInput struct {
	OrderID  string `json:"orderId"`
	Amount   *Money `json:"amount"`
	Currency string `json:"currency"`
	Card     *Card  `json:"card"`
}
type Transaction struct {
	TransactionID string `json:"transactionId"`
	OrderID       string `json:"orderId"`
	Status        string `json:"status"`
	Amount        Money  `json:"amount"`
	Currency      string `json:"currency"`
	CardLast4     string `json:"cardLast4"`
	CreatedAt     string `json:"createdAt"`
}
type entry struct {
	Transaction Transaction
	Fingerprint [32]byte
}
type Service struct {
	config         Config
	mu             sync.Mutex
	transactions   map[string]entry
	fingerprintKey [32]byte
	clock          func() time.Time
}

var ErrConflict = errors.New("idempotency_conflict")
var ErrInvalid = errors.New("invalid_request")
var ErrNotFound = errors.New("not_found")

func New(config Config, clock func() time.Time) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = time.Now
	}
	s := &Service{config: config, transactions: make(map[string]entry), clock: clock}
	if _, err := rand.Read(s.fingerprintKey[:]); err != nil {
		return nil, errors.New("no se pudo inicializar el servicio")
	}
	return s, nil
}
func digits(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func validID(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 128 }
func (input *ChargeInput) validate() error {
	if !validID(input.OrderID) || input.Amount == nil || *input.Amount < 0 || int64(*input.Amount) > MaxCents || input.Currency != "BOB" || input.Card == nil {
		return ErrInvalid
	}
	card := input.Card
	card.Holder = strings.TrimSpace(card.Holder)
	if !digits(card.Number, 13, 19) || !digits(card.CVC, 3, 4) || card.Holder == "" || len(card.Holder) > 320 || len(card.Expiry) != 5 || card.Expiry[2] != '/' || !digits(card.Expiry[:2], 2, 2) || !digits(card.Expiry[3:], 2, 2) {
		return ErrInvalid
	}
	month, _ := strconv.Atoi(card.Expiry[:2])
	if month < 1 || month > 12 {
		return ErrInvalid
	}
	return nil
}
func (s *Service) fingerprint(input ChargeInput) [32]byte {
	digest := hmac.New(sha256.New, s.fingerprintKey[:])
	for _, value := range []string{input.OrderID, strconv.FormatInt(int64(*input.Amount), 10), input.Currency, input.Card.Number, input.Card.Holder, input.Card.Expiry, input.Card.CVC} {
		_, _ = io.WriteString(digest, strconv.Itoa(len(value))+":"+value)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}
func (s *Service) Charge(input ChargeInput) (Transaction, error) {
	if input.Card != nil {
		card := *input.Card
		input.Card = &card
	}
	if input.Amount != nil {
		amount := *input.Amount
		input.Amount = &amount
	}
	if err := input.validate(); err != nil {
		return Transaction{}, err
	}
	fingerprint := s.fingerprint(input)
	// The repository stores only safe transaction data and a keyed fingerprint, not the card.
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.transactions[input.OrderID]; ok {
		if !hmac.Equal(existing.Fingerprint[:], fingerprint[:]) {
			return Transaction{}, ErrConflict
		}
		return existing.Transaction, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Transaction{}, errors.New("no se pudo procesar el pago")
	}
	last4 := strings.Clone(input.Card.Number[len(input.Card.Number)-4:])
	status := "approved"
	if last4 == "0000" {
		status = "declined"
	}
	transaction := Transaction{TransactionID: "pay-" + hex.EncodeToString(random[:]), OrderID: input.OrderID, Status: status, Amount: *input.Amount, Currency: input.Currency, CardLast4: last4, CreatedAt: s.clock().UTC().Format(time.RFC3339Nano)}
	s.transactions[input.OrderID] = entry{Transaction: transaction, Fingerprint: fingerprint}
	return transaction, nil
}
func (s *Service) ByOrder(id string) (Transaction, error) {
	if !validID(id) {
		return Transaction{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.transactions[id]
	if !ok {
		return Transaction{}, ErrNotFound
	}
	return existing.Transaction, nil
}
