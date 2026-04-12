// Command server starts the Open Democracy read-only web frontend.
package main

import (
	"bufio"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/server"
	"github.com/philspins/open-democracy/internal/store"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
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

// loadDotEnv reads KEY=VALUE pairs from a .env-style file if present.
// Existing process environment variables are not overwritten.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return scanner.Err()
}
