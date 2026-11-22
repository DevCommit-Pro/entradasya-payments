package payments

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	Port           int
	InternalAPIKey string
}

func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 || len(c.InternalAPIKey) < 32 {
		return errors.New("PORT debe ser válido e INTERNAL_API_KEY requiere al menos 32 bytes")
	}
	return nil
}
func ConfigFromEnv() (Config, error) {
	port := 4004
	if value, exists := os.LookupEnv("PORT"); exists {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("PORT no válido")
		}
		port = parsed
	}
	config := Config{Port: port, InternalAPIKey: os.Getenv("INTERNAL_API_KEY")}
	return config, config.Validate()
}
