package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	ctx   = context.Background()
	db    *sql.DB
	cache *redis.Client
)

// Cache expiration minutes
const cacheExpiration = time.Minute * 60

func main() {
	log.Println("Starting server")

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file", err)
	}

	db, err = sql.Open("postgres", os.Getenv("DATABASE"))
	if err != nil {
		log.Fatal("Error connecting to the database", err)
	}
	_, _ = db.Exec("CREATE TABLE uploads (id VARCHAR(36) PRIMARY KEY, cType VARCHAR(30), created date DEFAULT now(), bytes BYTEA)")

	redisAddr := os.Getenv("REDIS")
	if len(redisAddr) > 0 {
		cache = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: "",
			DB:       0, // default DB
		})
		pong, err := cache.Ping(ctx).Result()
		fmt.Println(pong, err)
	}

	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/file/", getFileHandler)

	port := os.Getenv("PORT")
	if len(port) == 0 {
		port = ":8080"
	}
	log.Println("Listening on port", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func uploadHandler(w http.ResponseWriter, req *http.Request) {
	// Allow CORS: * or specific origin
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	err := req.ParseMultipartForm(1000 * 1024)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, handler, err := req.FormFile("file")
	defer file.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cType := handler.Header.Get("Content-Type")
	src, err := uploadFile(file, cType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	body := UploadResponse{Data: []string{src}}
	json.NewEncoder(w).Encode(body)
}

type UploadResponse struct {
	Data []string
}

func getFileHandler(w http.ResponseWriter, req *http.Request) {
	id := req.RequestURI[len("/file/"):]

	bytes, cType, err := getFile(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", cType)
	w.Write(bytes)
}

func uploadFile(src io.Reader, cType string) (string, error) {
	id := uuid.New().String()

	bytes, err := ioutil.ReadAll(src)
	if err != nil {
		return "", err
	}

	_, err = db.Exec("INSERT INTO uploads (id, cType, bytes) VALUES ($1, $2, $3)", id, cType, bytes)
	if err != nil {
		return "", err
	}

	return os.Getenv("HOST") + "/file/" + id, nil
}

func getFile(id string) ([]byte, string, error) {

	var bytes []byte
	var cType string
	var err error

	if cache != nil {
		if checkCache(id, &bytes, &cType) {
			log.Println("Cache hit", id)
			return bytes, cType, nil
		} else {
			log.Println("Cache miss", id)
		}
	}

	row := db.QueryRow("SELECT bytes, cType FROM uploads WHERE id = $1", id)
	err = row.Scan(&bytes, &cType)
	if err != nil {
		log.Println("Database error", err)
	}

	if cache != nil {
		err = updateCache(id, cType, bytes)
		if err != nil {
			log.Println("Error with redis", err)
		} else {
			log.Println("New cache entry", id)
		}
	}

	return bytes, cType, err
}

func updateCache(id string, cType string, bytes []byte) error {
	err := cache.Set(ctx, "content_"+id, bytes, cacheExpiration).Err()
	if err != nil {
		return err
	}
	err = cache.Set(ctx, "type_"+id, cType, cacheExpiration).Err()
	return err
}

func checkCache(id string, bytes *[]byte, cType *string) bool {

	var err error

	*cType, err = cache.Get(ctx, "type_"+id).Result()
	if err == nil {
		*bytes, err = cache.Get(ctx, "content_"+id).Bytes()
		if err != nil {
			log.Println("Cache error", "content_"+id, err)
		}
		return true
	} else if err != redis.Nil {
		log.Println("Cache error", "type_"+id, err)
	}
	return false
}
