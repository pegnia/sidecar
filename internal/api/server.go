package api

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pegnia/sidecar/internal/config"
)

// Server holds dependencies and configuration for the internal API server.
type Server struct {
	listenAddr string
	dataRoot   string
	// root confines every file operation to dataRoot, including through symlinks the
	// game (or a customer's mod) may have created inside it.
	root   *os.Root
	apiKey string
	logger *slog.Logger

	stdoutLogPath string
	stdoutLogRel  string
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

// NewServer creates a new API server instance. The data root must exist.
func NewServer(cfg *config.Config) (*Server, error) {
	dataRoot := filepath.Clean(cfg.Data.Root)
	root, err := os.OpenRoot(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("open data root: %w", err)
	}
	rateLimit := cfg.API.RateLimit
	if rateLimit <= 0 {
		rateLimit = 60
	}
	return &Server{
		listenAddr:    cfg.API.ListenAddress,
		dataRoot:      dataRoot,
		root:          root,
		apiKey:        cfg.API.APIKey,
		logger:        slog.With("component", "api-server"),
		stdoutLogPath: filepath.Join(dataRoot, cfg.Data.StdoutFile),
		stdoutLogRel:  filepath.Clean(cfg.Data.StdoutFile),
		requestCounts: make(map[string]int),
		rateLimit:     rateLimit,
	}, nil
}

// responseWriter is a wrapper for http.ResponseWriter that captures the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	// Default to 200 OK if WriteHeader is not called
	return &responseWriter{w, http.StatusOK}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// loggingMiddleware logs incoming HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrappedWriter := newResponseWriter(w)

		next.ServeHTTP(wrappedWriter, r) // Serve the request

		s.logger.Info("HTTP Request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"status_code", wrappedWriter.statusCode,
			"duration", time.Since(start),
			"user_agent", r.UserAgent(),
		)
	})
}

// authMiddleware requires the configured API key in the X-API-Key header. Without a
// configured key (only allowed with SIDECAR_INSECURE=true) the API is open.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-API-Key")), []byte(s.apiKey)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitRequest implements a simple rate limiting middleware
func (s *Server) rateLimitRequest(next http.Handler) http.Handler {
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

// Handler returns the API with its middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.healthCheckHandler)
	mux.HandleFunc("GET /api/files", s.listFilesHandler)
	mux.HandleFunc("GET /api/files/download", s.downloadFileHandler)
	mux.HandleFunc("POST /api/files/upload", s.uploadFileHandler)
	mux.HandleFunc("POST /api/files/delete", s.deleteFileHandler)
	mux.HandleFunc("POST /api/files/create-dir", s.createDirHandler)

	mux.HandleFunc("GET /api/logs/stream", s.streamStdoutLogHandler)

	// Create a handler chain with our middleware. Order matters: requests flow from bottom to top.
	var handler http.Handler = mux
	handler = s.rateLimitRequest(handler)
	handler = s.authMiddleware(handler)
	handler = s.loggingMiddleware(handler)
	return handler
}

// Run serves the API until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	defer s.root.Close()
	srv := &http.Server{
		Addr:              s.listenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		s.logger.Info("Starting file manager API server", "address", srv.Addr, "serving_from", s.dataRoot)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	s.logger.Info("Shutting down API server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// sanitizePath turns a user-provided path into a path relative to the data root.
// Paths are always taken relative to the root: "/", "" and "." mean the root itself, and
// "/mods" and "mods" are the same directory. A path that would leave the root, such as
// "../etc", "/../etc" or "../data-other", is rejected. File operations additionally go through
// os.Root, which also stops symlinks inside the data directory from leading outside it.
func sanitizePath(userPath string) (string, error) {
	if strings.ContainsRune(userPath, 0) {
		return "", errInvalidPath
	}
	rel := strings.TrimLeft(filepath.FromSlash(userPath), string(filepath.Separator))
	rel = filepath.Clean(rel) // "" becomes "."
	if rel != "." && !filepath.IsLocal(rel) {
		return "", errInvalidPath
	}
	return rel, nil
}

var errInvalidPath = errors.New("invalid path: access denied")

// fileError maps an error from a file operation to an HTTP response.
func (s *Server) fileError(w http.ResponseWriter, op, path string, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "Not found", http.StatusNotFound)
	case errors.Is(err, fs.ErrExist):
		http.Error(w, "Already exists", http.StatusConflict)
	case isEscape(err):
		// os.Root refuses paths that resolve outside the root, e.g. through a symlink.
		http.Error(w, errInvalidPath.Error(), http.StatusBadRequest)
	default:
		s.logger.Error("File operation failed", "op", op, "path", path, "error", err)
		http.Error(w, "Could not "+op, http.StatusInternalServerError)
	}
}

func isEscape(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "escapes from parent")
}

// listFilesHandler handles requests to list directory contents.
func (s *Server) listFilesHandler(w http.ResponseWriter, r *http.Request) {
	rel, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dir, err := s.root.Open(rel)
	if err != nil {
		s.fileError(w, "read directory", rel, err)
		return
	}
	defer dir.Close()
	dirEntries, err := dir.ReadDir(-1)
	if err != nil {
		s.fileError(w, "read directory", rel, err)
		return
	}

	files := []FileInfo{}
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
	rel, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f, err := s.root.Open(rel)
	if err != nil {
		s.fileError(w, "access file", rel, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.fileError(w, "access file", rel, err)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot download a directory", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// uploadFileHandler handles multipart file uploads.
func (s *Server) uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	rel, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check that the destination is an existing directory.
	dirInfo, err := s.root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "Destination directory does not exist", http.StatusBadRequest)
			return
		}
		s.fileError(w, "access destination directory", rel, err)
		return
	}
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

	// The file name must be a plain name, not a path.
	filename := header.Filename
	if filename == "" || filename != filepath.Base(filename) || !filepath.IsLocal(filename) {
		http.Error(w, "Invalid destination filename", http.StatusBadRequest)
		return
	}

	// Basic file type validation - check file extension
	ext := strings.ToLower(filepath.Ext(filename))
	dangerousExts := map[string]bool{
		".exe": true, ".dll": true, ".sh": true, ".bat": true, ".cmd": true,
		".php": true, ".phtml": true, ".js": true, ".jsp": true, ".asp": true,
	}
	if dangerousExts[ext] {
		s.logger.Warn("Attempted upload of potentially dangerous file type", "filename", filename, "extension", ext)
		http.Error(w, "File type not allowed for security reasons", http.StatusBadRequest)
		return
	}

	destRel := filepath.Join(rel, filename)
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if r.URL.Query().Get("overwrite") != "true" {
		flags |= os.O_EXCL
	}
	dst, err := s.root.OpenFile(destRel, flags, 0644)
	if errors.Is(err, fs.ErrExist) {
		http.Error(w, "File already exists. Use overwrite=true to replace it.", http.StatusConflict)
		return
	}
	if err != nil {
		s.fileError(w, "save file", destRel, err)
		return
	}
	defer dst.Close()

	s.logger.Info("File upload in progress",
		"filename", filename,
		"size", header.Size,
		"destination", destRel,
		"client_ip", r.RemoteAddr)

	if _, err := io.Copy(dst, file); err != nil {
		s.logger.Error("Failed to copy uploaded file content", "path", destRel, "error", err)
		http.Error(w, "Could not save file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, "File uploaded successfully")
	s.logger.Info("File upload completed successfully", "path", destRel, "size", header.Size)
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

	rel, err := sanitizePath(payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Important safety check: Do not allow deletion of the root directory itself.
	if rel == "." {
		http.Error(w, "Cannot delete root directory", http.StatusBadRequest)
		return
	}

	if err := removeAll(s.root, rel); err != nil {
		s.fileError(w, "delete item", rel, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Item deleted successfully")
}

// removeAll removes name and, if it is a directory, everything below it. Symlinks are
// removed, never followed. (os.Root has no RemoveAll before Go 1.25.)
func removeAll(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		dir, err := root.Open(name)
		if err != nil {
			return err
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := removeAll(root, filepath.Join(name, e.Name())); err != nil {
				return err
			}
		}
	}
	return root.Remove(name)
}

// createDirHandler creates a new directory, including missing parents.
func (s *Server) createDirHandler(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rel, err := sanitizePath(payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := mkdirAll(s.root, rel); err != nil {
		s.fileError(w, "create directory", rel, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, "Directory created successfully")
}

// mkdirAll creates name and its missing parents inside root. (os.Root has no MkdirAll
// before Go 1.25.)
func mkdirAll(root *os.Root, name string) error {
	if name == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		err := root.Mkdir(current, 0755)
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, statErr := root.Stat(current)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return &fs.PathError{Op: "mkdir", Path: current, Err: syscall.ENOTDIR}
		}
	}
	return nil
}

func (s *Server) streamStdoutLogHandler(w http.ResponseWriter, r *http.Request) {
	const initialLogLines = 100

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
	file, err := s.root.Open(s.stdoutLogRel)
	if err != nil {
		log.Error("Could not open log file for streaming", "error", err)
		http.Error(w, "Log file not available", http.StatusNotFound)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var history []string
	for scanner.Scan() {
		history = append(history, scanner.Text())
		// If our history buffer is too long, trim the oldest line from the front.
		if len(history) > initialLogLines {
			history = history[1:]
		}
	}

	if err := scanner.Err(); err != nil {
		log.Error("Error reading historical log lines", "error", err)
	}

	log.Info("Sending historical log lines", "count", len(history))
	if len(history) > 0 {
		for _, line := range history {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		fmt.Fprintf(w, "data: --- End of recent logs. Live stream starting... ---\n\n")
		flusher.Flush()
	}
	reader := bufio.NewReader(file)

	for {
		select {
		case <-r.Context().Done():
			log.Info("Client disconnected from log stream.")
			return
		default:
			line, err := reader.ReadString('\n')
			if err == io.EOF {
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
