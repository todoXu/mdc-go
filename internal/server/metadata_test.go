package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActorMappingVerification(t *testing.T) {
	document, err := actorMappingDocument("https://github.com/example/actors")
	if err != nil || document != "https://raw.githubusercontent.com/example/actors/main/mapping_actor.xml" {
		t.Fatalf("repository URL: %q, %v", document, err)
	}
	if _, err := actorMappingDocument("file:///mapping_actor.xml"); err == nil {
		t.Fatal("file URL accepted")
	}

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mapping_actor.xml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<actor><a zh_cn="示例" zh_tw="示例" jp="例" keyword="Example" /></actor>`))
	}))
	defer remote.Close()
	server, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"url": remote.URL + "/mapping_actor.xml"})
	response := call(server, "POST", "/api/config/metadata/verify-actor-mapping", string(body))
	if response.Code != 200 {
		t.Fatalf("verification failed: %d %s", response.Code, response.Body.String())
	}
}
