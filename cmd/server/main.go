// Command server starts the Open Democracy read-only web frontend.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/server"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/utils"
)

func main() {
	if err := utils.LoadDotEnv(".env"); err != nil {
		log.Printf("warning: could not load .env: %v", err)
	}

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

	log.Printf("Open Democracy listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server: %v", err)
	}
}
