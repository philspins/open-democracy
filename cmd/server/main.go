// Command server starts the CivicTracker read-only web frontend.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/server"
	"github.com/philspins/open-democracy/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", db.DefaultPath, "SQLite database path")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	st := store.New(conn)
	srv := server.New(st)

	log.Printf("CivicTracker listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server: %v", err)
	}
}
