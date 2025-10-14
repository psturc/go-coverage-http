package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	log.Println("Application starting...")

	http.HandleFunc("/health", HealthHandler)
	http.HandleFunc("/greet", GreetHandler)
	http.HandleFunc("/calculate", CalculateHandler)

	log.Println("Server running on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// Greet returns a greeting message
func Greet(name string) string {
	if name == "" {
		return "Hello, stranger!"
	}
	return "Hello, " + name + "!"
}

// Calculate performs addition
func Calculate(a, b int) int {
	if a < 0 || b < 0 {
		return 0
	}
	return a + b
}

// HealthHandler handles health check requests
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy"}`)
}

// GreetHandler handles greeting requests
func GreetHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	greeting := Greet(name)
	fmt.Fprintf(w, `{"message":"%s"}`, greeting)
}

// CalculateHandler handles calculation requests
func CalculateHandler(w http.ResponseWriter, r *http.Request) {
	result := Calculate(5, 10)
	fmt.Fprintf(w, `{"result":%d}`, result)
}
