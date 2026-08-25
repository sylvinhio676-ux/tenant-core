// Command httponly is Load Test Scenario A: a minimal net/http server
// with ZERO dependency on tenant-core. It exists purely to measure the
// ceiling of the load-testing machine and tool (Vegeta) themselves — any
// throughput number below this baseline says something about the
// generator or the hardware, not about tenant-core, and Scenarios B and
// C should always be read relative to this number, never in isolation.
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	log.Println("Scenario A (httponly baseline) listening on :8081")
	log.Println("try: curl http://localhost:8081/ping")

	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatal(err)
	}
}
