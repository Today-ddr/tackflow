package auth

import "testing"

func TestPasskeyReliesOnConfiguredHTTPSOrigin(t *testing.T) {
	for _, scenario := range []struct {
		url, rpID, origin string
	}{
		{"https://tack.example:8443/path", "tack.example", "https://tack.example:8443"},
		{"http://localhost:8080", "localhost", "http://localhost:8080"},
	} {
		relyingParty, err := NewPasskey(scenario.url)
		if err != nil {
			t.Fatal(err)
		}
		if relyingParty.Config.RPID != scenario.rpID || len(relyingParty.Config.RPOrigins) != 1 || relyingParty.Config.RPOrigins[0] != scenario.origin {
			t.Fatalf("wrong RP configuration: %+v", relyingParty.Config)
		}
	}
	for _, invalid := range []string{"", "/relative", "http://tack.example", "https://user:password@tack.example", "https://tack.example?host=evil.example", "https://tack.example#fragment", "https://127.0.0.1"} {
		if _, err := NewPasskey(invalid); err == nil {
			t.Errorf("accepted invalid public URL: %q", invalid)
		}
	}
}
