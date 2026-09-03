package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"cf-herdr-poc/internal/model"
)

func TestSandboxViewOmitsPrivateStateAndSanitizesText(t *testing.T) {
	secret := "top-secret-token"
	sandbox := model.Sandbox{
		Name: "demo", AppGUID: "private-guid", Repository: "https://git.example/demo.git", Revision: "abc123",
		Buildpack: "ruby_buildpack", Desired: model.DesiredPresent, Phase: model.PhaseFailed,
		ResumePhase: model.PhaseStaging, InternalHost: "demo.identity.example", PackMemberID: "demo",
		LastError:  "authorization: Bearer " + secret,
		Operations: []model.Operation{{Name: "stage", Command: "cf push --token " + secret, Summary: "token=" + secret, Error: "password=" + secret, Duration: time.Second}},
		CreatedAt:  time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(20, 0).UTC(),
	}

	view := PublicSandbox(sandbox)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"private-guid", "demo.identity.example", secret, "command", "appGuid", "internalHost"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public JSON contains forbidden value %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"demo", "abc123", "ruby_buildpack", "packMemberId", "resumePhase", "[REDACTED]"} {
		if !strings.Contains(text, required) {
			t.Fatalf("public JSON missing %q: %s", required, text)
		}
	}

	typ := reflect.TypeOf(view)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		for _, forbidden := range []string{"appGuid", "internalHost", "route", "policy", "token", "file", "command"} {
			if strings.Contains(strings.ToLower(field.Name), forbidden) || strings.Contains(strings.ToLower(name), forbidden) {
				t.Fatalf("SandboxView exposes forbidden field %s (%s)", field.Name, name)
			}
		}
	}
}
