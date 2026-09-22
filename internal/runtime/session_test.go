package runtime

import (
	"net/http"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"

	"github.com/BonzTM/bloom/internal/config"
)

func TestConfigureSessionsCookieSecurity(t *testing.T) {
	tests := []struct {
		name   string
		secure bool
	}{
		{name: "secure default", secure: true},
		{name: "development override", secure: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			manager := configureSessions(config.AuthConfig{
				SessionCookieSecure: testCase.secure,
				SessionLifetime:     time.Hour,
				SessionIdleTimeout:  15 * time.Minute,
			}, memstore.NewWithCleanupInterval(0))
			if manager.Cookie.Secure != testCase.secure || !manager.Cookie.HttpOnly ||
				manager.Cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie options = %+v", manager.Cookie)
			}
			if manager.Cookie.Name != "bloom_session" || manager.Cookie.Path != "/" {
				t.Fatalf("cookie identity = %+v", manager.Cookie)
			}
		})
	}
}
