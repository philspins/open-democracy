// Command netlify runs Open Democracy as a Netlify Function.
package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/server"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/utils"
)

var (
	initOnce sync.Once
	proxy    *httpadapter.HandlerAdapter
	initErr  error
)

func initServer() {
	if err := utils.LoadDotEnv(".env"); err != nil {
		log.Printf("warning: could not load .env: %v", err)
	}

	dbPath, err := resolveDBPath()
	if err != nil {
		initErr = err
		return
	}

	conn, err := db.Open(dbPath)
	if err != nil {
		initErr = err
		return
	}

	st := store.New(conn)
	srv := server.New(st)
	proxy = httpadapter.New(srv)
}

func resolveDBPath() (string, error) {
	dbPath := strings.TrimSpace(os.Getenv("OPEN_DEMOCRACY_DB_PATH"))
	if dbPath == "" {
		dbPath = strings.TrimSpace(os.Getenv("DB_PATH"))
	}
	if dbPath != "" {
		return dbPath, nil
	}

	// Netlify/AWS Lambda functions can only write under /tmp.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		tmpPath := filepath.Join(os.TempDir(), db.DefaultPath)
		if _, err := os.Stat(tmpPath); err == nil {
			return tmpPath, nil
		}
		if err := copyFileIfExists(db.DefaultPath, tmpPath); err != nil {
			return "", err
		}
		return tmpPath, nil
	}

	return db.DefaultPath, nil
}

func copyFileIfExists(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	initOnce.Do(initServer)
	if initErr != nil {
		log.Printf("failed to initialize server: %v", initErr)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Headers: map[string]string{
				"Content-Type": "text/plain; charset=utf-8",
			},
			Body: "internal server error",
		}, nil
	}

	return proxy.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
