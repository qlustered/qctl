package config

import "testing"

func TestResolveServerWithSourcePrecedence(t *testing.T) {
	cfg := &Config{CurrentContext: "staging", Contexts: map[string]*Context{"staging": {Server: "https://context.example"}}}
	for _, tt := range []struct {
		name       string
		flag       string
		env        string
		wantServer string
		wantSource string
		wantToken  ServerSource
	}{
		{"flag", "https://flag.example", "https://env.example", "https://flag.example", "from the --server flag", ServerSourceFlag},
		{"env", "", "https://env.example", "https://env.example", "from QCTL_SERVER", ServerSourceEnv},
		{"context", "", "", "https://context.example", `from context "staging"`, ServerSourceContext},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QCTL_SERVER", tt.env)
			server, source, err := ResolveServerWithSource(cfg, tt.flag)
			if err != nil || server != tt.wantServer || source != tt.wantSource {
				t.Fatalf("got (%q, %q, %v)", server, source, err)
			}
			server, token, err := SelectServer(cfg, tt.flag)
			if err != nil || server != tt.wantServer || token != tt.wantToken {
				t.Fatalf("SelectServer got (%q, %q, %v)", server, token, err)
			}
		})
	}
}
