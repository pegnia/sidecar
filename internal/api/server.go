package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server holds dependencies and configuration for the internal API server.
type Server struct {
	listenAddr string
	dataRoot   string
	logger     *slog.Logger

	stdoutLogPath string
	requestCounts map[string]int
	rateLimitMu   sync.Mutex
	rateLimit     int
}

// FileInfo represents a single file or directory, used for JSON responses.
type FileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
}

// NewServer creates a new API server instance.
func NewServer(listenAddr, dataRoot string, stdoutFile string) *Server {

	// Get rate limit from environment variable, default to 60 requests per minute
	rateLimit := 60
	if rateLimitEnv := os.Getenv("SIDECAR_RATE_LIMIT"); rateLimitEnv != "" {
		if val, err := strconv.Atoi(rateLimitEnv); err == nil && val > 0 {
			rateLimit = val
		}
	}

	return &Server{
		listenAddr:    listenAddr,
		dataRoot:      dataRoot,
		logger:        slog.With("component", "api-server"),
		stdoutLogPath: filepath.Join(dataRoot, stdoutFile),
		requestCounts: make(map[string]int),
		rateLimit:     rateLimit,
	}
}

// rateLimitRequest implements a simple rate limiting middleware
func (s *Server) rateLimitRequest(next http.Handler) http.Handler {
	// FIXME: This does not work
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health check endpoint
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Get client IP
		ip := r.RemoteAddr
		if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
			ip = strings.Split(forwardedFor, ",")[0]
		}

		// Check rate limit
		s.rateLimitMu.Lock()
		count := s.requestCounts[ip]

		// If this is the first request in this minute, reset the count
		// This is a simplified approach - a production system would use a proper time window
		if count == 0 {
			// Start a goroutine to reset the count after 1 minute
			go func(clientIP string) {
				time.Sleep(time.Minute)
				s.rateLimitMu.Lock()
				delete(s.requestCounts, clientIP)
				s.rateLimitMu.Unlock()
			}(ip)
		}

		// Increment the count
		s.requestCounts[ip] = count + 1
		exceeded := count >= s.rateLimit
		s.rateLimitMu.Unlock()

		if exceeded {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Run starts the HTTP server and handles graceful shutdown.
func (s *Server) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.healthCheckHandler)
	mux.HandleFunc("GET /api/files", s.listFilesHandler)
	mux.HandleFunc("GET /api/files/download", s.downloadFileHandler)
	mux.HandleFunc("POST /api/files/upload", s.uploadFileHandler)
	mux.HandleFunc("POST /api/files/delete", s.deleteFileHandler)
	mux.HandleFunc("POST /api/files/create-dir", s.createDirHandler)

	mux.HandleFunc("GET /api/logs/stream", s.streamStdoutLogHandler)

	// Create a handler chain with our middleware
	var handler http.Handler = mux
	handler = s.rateLimitRequest(handler)

	srv := &http.Server{
		Addr:    s.listenAddr,
		Handler: handler,
	}

	go func() {
		s.logger.Info("Starting file manager API server", "address", srv.Addr, "serving_from", s.dataRoot)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("API server crashed", "error", err)
		}
	}()

	<-ctx.Done()
	s.logger.Info("Shutting down API server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("API server graceful shutdown failed", "error", err)
	}
}

// sanitizePath cleans and validates a user-provided path.
// It ensures the resulting path is a clean, absolute path still within the allowed dataRoot.
// This is the primary security function to prevent path traversal attacks.
func (s *Server) sanitizePath(userPath string) (string, error) {
	// Join the root with the user-provided path and clean it up (resolves .., ., //, etc.)
	fullPath := filepath.Join(s.dataRoot, userPath)

	// Security check: ensure the final, cleaned path still starts with our root directory.
	if !strings.HasPrefix(fullPath, s.dataRoot) {
		return "", fmt.Errorf("invalid path: access denied")
	}
	return fullPath, nil
}

// listFilesHandler handles requests to list directory contents.
func (s *Server) listFilesHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	fullPath, err := s.sanitizePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dirEntries, err := os.ReadDir(fullPath)
	if err != nil {
		s.logger.Error("Failed to read directory", "path", fullPath, "error", err)
		http.Error(w, "Could not read directory", http.StatusInternalServerError)
		return
	}

	var files []FileInfo
	for _, entry := range dirEntries {
		info, err := entry.Info()
		if err != nil {
			s.logger.Warn("Could not get file info for entry, skipping", "entry", entry.Name(), "error", err)
			continue
		}
		files = append(files, FileInfo{
			Name:     info.Name(),
			Size:     info.Size(),
			IsDir:    info.IsDir(),
			Modified: info.ModTime(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(files); err != nil {
		s.logger.Error("Failed to encode file list to JSON", "error", err)
	}
}

// downloadFileHandler serves a single file for download.
func (s *Server) downloadFileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	fullPath, err := s.sanitizePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Could not access file", http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		http.Error(w, "Cannot download a directory", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(fullPath))
	http.ServeFile(w, r, fullPath)
}

// uploadFileHandler handles multipart file uploads.
func (s *Server) uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	fullPath, err := s.sanitizePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if destination directory exists
	dirInfo, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Destination directory does not exist", http.StatusBadRequest)
		} else {
			s.logger.Error("Failed to check destination directory", "path", fullPath, "error", err)
			http.Error(w, "Could not access destination directory", http.StatusInternalServerError)
		}
		return
	}

	// Ensure the destination is a directory
	if !dirInfo.IsDir() {
		http.Error(w, "Destination path is not a directory", http.StatusBadRequest)
		return
	}

	// Limit upload size (e.g., 500 MB) to prevent abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 500*1024*1024)

	file, header, err := r.FormFile("file")
	if err != nil {
		s.logger.Warn("Invalid file upload attempt", "error", err)
		http.Error(w, "Invalid file upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Basic file type validation - check file extension
	// This is a simple example - a production system would use more robust validation
	filename := header.Filename
	ext := strings.ToLower(filepath.Ext(filename))

	// List of potentially dangerous extensions
	dangerousExts := map[string]bool{
		".exe": true, ".dll": true, ".sh": true, ".bat": true, ".cmd": true,
		".php": true, ".phtml": true, ".js": true, ".jsp": true, ".asp": true,
	}

	if dangerousExts[ext] {
		s.logger.Warn("Attempted upload of potentially dangerous file type", "filename", filename, "extension", ext)
		http.Error(w, "File type not allowed for security reasons", http.StatusBadRequest)
		return
	}

	destPath := filepath.Join(fullPath, filename)

	// Final security check on the combined path to ensure no funny business in the filename.
	if !strings.HasPrefix(destPath, fullPath) {
		http.Error(w, "Invalid destination filename", http.StatusBadRequest)
		return
	}

	// Check if file already exists
	overwrite := r.URL.Query().Get("overwrite") == "true"
	if _, err := os.Stat(destPath); err == nil && !overwrite {
		http.Error(w, "File already exists. Use overwrite=true to replace it.", http.StatusConflict)
		return
	}

	// Create the file with appropriate permissions
	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		s.logger.Error("Failed to create file for upload", "path", destPath, "error", err)
		http.Error(w, "Could not save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Log the upload attempt
	s.logger.Info("File upload in progress",
		"filename", filename,
		"size", header.Size,
		"destination", destPath,
		"client_ip", r.RemoteAddr)

	if _, err := io.Copy(dst, file); err != nil {
		s.logger.Error("Failed to copy uploaded file content", "path", destPath, "error", err)
		http.Error(w, "Could not save file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, "File uploaded successfully")
	s.logger.Info("File upload completed successfully", "path", destPath, "size", header.Size)
}

// deleteFileHandler deletes a file or directory recursively.
func (s *Server) deleteFileHandler(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	fullPath, err := s.sanitizePath(payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Important safety check: Do not allow deletion of the root directory itself.
	if fullPath == s.dataRoot {
		http.Error(w, "Cannot delete root directory", http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		s.logger.Error("Failed to delete file/directory", "path", fullPath, "error", err)
		http.Error(w, "Could not delete item", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Item deleted successfully")
}

// createDirHandler creates a new directory.
func (s *Server) createDirHandler(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	fullPath, err := s.sanitizePath(payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		s.logger.Error("Failed to create directory", "path", fullPath, "error", err)
		http.Error(w, "Could not create directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, "Directory created successfully")
}

func (s *Server) streamStdoutLogHandler(w http.ResponseWriter, r *http.Request) {
	log := s.logger.With("handler", "streamStdoutLog", "path", s.stdoutLogPath)
	log.Info("Log stream connection initiated.")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Adjust for production if needed

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Error("Streaming unsupported by the connection")
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// For a more robust solution, consider a library like "github.com/nxadm/tail"
	// but for simplicity, a basic tailing loop is shown here.
	file, err := os.Open(s.stdoutLogPath)
	if err != nil {
		log.Error("Could not open log file for streaming", "error", err)
		http.Error(w, "Log file not available", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Start reading from the end of the file to only get new lines.
	file.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(file)

	for {
		select {
		case <-r.Context().Done():
			log.Info("Client disconnected from log stream.")
			return // Exit when the client closes the connection.
		default:
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				// No new lines, wait a moment and try again.
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if err != nil {
				log.Warn("Error reading from log file during stream", "error", err)
				return
			}

			// Format as an SSE message ("data: ...\n\n").
			fmt.Fprintf(w, "data: %s\n\n", strings.TrimSpace(line))

			// Flush the data to the client immediately.
			flusher.Flush()
		}
	}
}

// healthCheckHandler provides a simple endpoint to verify the server is running.
func (s *Server) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "OK")
}
