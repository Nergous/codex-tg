package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
)

const (
	openPath   = "/v1/open"
	statusPath = "/v1/status"
	stopPath   = "/v1/stop"
)

var (
	ErrInvalidIPCConfig = errors.New("invalid IPC configuration")
)

type Service interface {
	Open(ctx context.Context, request OpenRequest) (OpenResponse, error)
	Status(ctx context.Context) (StatusResponse, error)
	Stop(ctx context.Context) error
}

type OpenRequest struct {
	ProjectPath string `json:"project_path"`
	NewSession  bool   `json:"new"`
}

type OpenResponse struct {
	ThreadID string `json:"thread_id"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

type StatusResponse struct {
	ThreadID    string `json:"thread_id"`
	ProjectPath string `json:"project_path"`
	Running     bool   `json:"running"`
}

type Server struct {
	service Service
	token   string
	server  *http.Server
	addr    string
}

func NewServer(service Service, authToken string) *Server {
	return &Server{
		service: service,
		token:   authToken,
	}
}

func (s *Server) Start(_ context.Context) (string, error) {
	if s.service == nil || s.token == "" {
		return "", fmt.Errorf("%w: missing service or token", ErrInvalidIPCConfig)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	s.addr = "http://" + listener.Addr().String()
	s.server = &http.Server{Handler: s.handler()}
	go func() {
		_ = s.server.Serve(listener)
	}()
	return s.addr, nil
}

func (s *Server) Address() string {
	return s.addr
}

func (s *Server) Close() error {
	if s.server == nil {
		return nil
	}
	return s.server.Close()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(openPath, s.handleOpen)
	mux.HandleFunc(statusPath, s.handleStatus)
	mux.HandleFunc(stopPath, s.handleStop)
	return mux
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if err := s.authorize(r); err != nil {
		respondError(w, err)
		return
	}
	if err := ensureMethod(w, r, http.MethodPost); err != nil {
		respondError(w, err)
		return
	}

	var req OpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, badRequest("invalid request body"))
		return
	}
	if err := validateOpenRequest(req); err != nil {
		respondError(w, err)
		return
	}

	response, err := s.service.Open(r.Context(), req)
	if err != nil {
		respondError(w, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.authorize(r); err != nil {
		respondError(w, err)
		return
	}
	if err := ensureMethod(w, r, http.MethodGet); err != nil {
		respondError(w, err)
		return
	}

	response, err := s.service.Status(r.Context())
	if err != nil {
		respondError(w, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.authorize(r); err != nil {
		respondError(w, err)
		return
	}
	if err := ensureMethod(w, r, http.MethodPost); err != nil {
		respondError(w, err)
		return
	}
	if err := s.service.Stop(r.Context()); err != nil {
		respondError(w, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) authorize(r *http.Request) error {
	if r.Header.Get("Origin") != "" {
		return forbidden("origin header rejected")
	}
	if !isLoopbackAddress(r.RemoteAddr) {
		return forbidden("non-loopback peer rejected")
	}

	auth := r.Header.Get("Authorization")
	want := "Bearer " + s.token
	if auth != want {
		return unauthorized("invalid token")
	}
	return nil
}

func isLoopbackAddress(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func validateOpenRequest(req OpenRequest) error {
	path := strings.TrimSpace(req.ProjectPath)
	if path == "" {
		return badRequest("project path is required")
	}
	if strings.Contains(path, "\x00") {
		return badRequest("project path is invalid")
	}
	if !filepath.IsAbs(path) {
		return badRequest("project path must be absolute")
	}
	rawSlashed := strings.ReplaceAll(path, "\\", "/")
	for component := range strings.SplitSeq(rawSlashed, "/") {
		if component == ".." {
			return badRequest("project path is invalid")
		}
	}
	cleaned := filepath.Clean(path)
	slashed := strings.ReplaceAll(cleaned, "\\", "/")
	if strings.Contains(slashed, "/../") || strings.HasSuffix(slashed, "/..") {
		return badRequest("project path is invalid")
	}
	if !filepath.IsAbs(cleaned) {
		return badRequest("project path is invalid")
	}
	return nil
}

func ensureMethod(w http.ResponseWriter, r *http.Request, want string) error {
	if r.Method != want {
		return methodNotAllowed(want)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		respondError(w, internalError(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func respondError(w http.ResponseWriter, err error) {
	var apiErr statusError
	if errors.As(err, &apiErr) {
		http.Error(w, apiErr.Message, apiErr.Code)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

type statusError struct {
	Code    int
	Message string
}

func (e statusError) Error() string { return e.Message }

func badRequest(message string) error {
	return statusError{Code: http.StatusBadRequest, Message: message}
}
func forbidden(message string) error {
	return statusError{Code: http.StatusForbidden, Message: message}
}
func unauthorized(message string) error {
	return statusError{Code: http.StatusUnauthorized, Message: message}
}
func internalError(message string) error {
	return statusError{Code: http.StatusInternalServerError, Message: message}
}
func methodNotAllowed(message string) error {
	return statusError{Code: http.StatusMethodNotAllowed, Message: message}
}
