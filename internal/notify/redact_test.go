package notify

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jgalea/amz/internal/config"
)

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: no route to host")
}

func TestTelegramErrorHidesToken(t *testing.T) {
	const tok = "123456:SENTINEL-TOKEN-VALUE"
	t.Setenv("AMZ_TEST_TG", tok)
	old := client.Transport
	client.Transport = failing{}
	defer func() { client.Transport = old }()
	var s config.Notify
	s.Telegram.TokenEnv = "AMZ_TEST_TG"
	s.Telegram.ChatID = "1"
	err := telegram(s, "t", "b")
	if err == nil {
		t.Fatal("want an error from the failing transport")
	}
	if strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("token leaked: %v", err)
	}
}
