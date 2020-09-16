package main

import (
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

var (
	db *sql.DB
)

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
	_, _ = db.Exec("CREATE TABLE uploads (id SERIAL, fileId VARCHAR(36), cType VARCHAR(30), created date DEFAULT now(), bytes BYTEA)")

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

	body := UploadResponse{Data: []string{src}}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

type UploadResponse struct {
	Data []string
}

func getFileHandler(w http.ResponseWriter, req *http.Request) {
	fileID := req.RequestURI[len("/file/"):]

	bytes, cType, err := getFile(fileID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", cType)
	w.Write(bytes)
}

func uploadFile(src io.Reader, cType string) (string, error) {
	fileID := uuid.New().String()
	bytes, err := ioutil.ReadAll(src)

	_, err = db.Exec("INSERT INTO uploads (fileId, cType, bytes) VALUES ($1, $2, $3)", fileID, cType, bytes)
	if err != nil {
		return "", err
	}
	
	return os.Getenv("HOST") + "/file/" + fileID, nil
}

func getFile(fileID string) ([]byte, string, error) {

	row := db.QueryRow("SELECT bytes, cType FROM uploads WHERE fileID = $1", fileID)

	var bytes []byte
	var cType string

	err := row.Scan(&bytes, &cType)

	return bytes, cType, err
}
