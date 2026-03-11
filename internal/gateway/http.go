package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Melon-cream/mcp-estuary/internal/mcp"
)

type Backend interface {
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (mcp.CallToolResult, error)
	Stats() map[string]any
	StopAll(ctx context.Context) error
}

type Server struct {
	logger   *log.Logger
	backend  Backend
	sessions *sessionStore
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
}

type session struct {
	ID              string
	ProtocolVersion string
	Initialized     bool
	CreatedAt       time.Time
}

func NewServer(logger *log.Logger, backend Backend) *Server {
	return &Server{
		logger:  logger,
		backend: backend,
		sessions: &sessionStore{
			sessions: make(map[string]session),
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/mcp", s.handleMCP)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.backend.Stats())
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleSSE(w, r)
	case http.MethodDelete:
		s.handleDeleteSession(w, r)
	case http.MethodPost:
		s.handlePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if sessionID := r.Header.Get("MCP-Session-Id"); sessionID != "" {
		if _, ok := s.sessions.get(sessionID); !ok {
			http.Error(w, "unknown session", http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing MCP-Session-Id header", http.StatusBadRequest)
		return
	}
	s.sessions.delete(sessionID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	var msg mcp.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if msg.JSONRPC == "" {
		msg.JSONRPC = "2.0"
	}
	if msg.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, mcp.NewErrorResponse(msg.ID, -32600, "jsonrpc must be 2.0"))
		return
	}
	switch msg.Method {
	case "initialize":
		s.handleInitialize(w, msg)
	case "notifications/initialized":
		s.handleInitialized(w, r, msg)
	case "ping":
		s.withSession(w, r, msg, func(session session) {
			s.writeResponse(w, session, mcp.NewResponse(msg.ID, map[string]any{}))
		})
	case "tools/list":
		s.withSession(w, r, msg, func(session session) {
			tools, err := s.backend.ListTools(r.Context())
			if err != nil {
				s.writeResponse(w, session, mcp.NewErrorResponse(msg.ID, -32000, err.Error()))
				return
			}
			s.writeResponse(w, session, mcp.NewResponse(msg.ID, mcp.ListToolsResult{Tools: tools}))
		})
	case "tools/call":
		s.withSession(w, r, msg, func(session session) {
			var params mcp.CallToolParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				s.writeResponse(w, session, mcp.NewErrorResponse(msg.ID, -32602, "invalid tools/call params"))
				return
			}
			if params.Arguments == nil {
				params.Arguments = map[string]any{}
			}
			result, err := s.backend.CallTool(r.Context(), params.Name, params.Arguments)
			if err != nil {
				s.writeResponse(w, session, mcp.NewErrorResponse(msg.ID, -32000, err.Error()))
				return
			}
			s.writeResponse(w, session, mcp.NewResponse(msg.ID, result))
		})
	case "":
		w.WriteHeader(http.StatusAccepted)
	default:
		s.withSession(w, r, msg, func(session session) {
			s.writeResponse(w, session, mcp.NewErrorResponse(msg.ID, -32601, fmt.Sprintf("method %q not found", msg.Method)))
		})
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, msg mcp.Message) {
	var params mcp.InitializeParams
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			writeJSON(w, http.StatusBadRequest, mcp.NewErrorResponse(msg.ID, -32602, "invalid initialize params"))
			return
		}
	}
	if params.ProtocolVersion == "" {
		params.ProtocolVersion = mcp.ProtocolVersion
	}
	session := s.sessions.create(params.ProtocolVersion)
	w.Header().Set("MCP-Session-Id", session.ID)
	w.Header().Set("MCP-Protocol-Version", session.ProtocolVersion)
	writeJSON(w, http.StatusOK, mcp.NewResponse(msg.ID, mcp.InitializeResult{
		ProtocolVersion: session.ProtocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{"listChanged": false}},
		ServerInfo:      mcp.ServerInfo{Name: "mcp-estuary", Title: "mcp-estuary gateway", Version: "0.1.0"},
		Instructions:    "Tool names are exposed as <server>__<tool> to avoid collisions across upstream MCP servers.",
	}))
}

func (s *Server) handleInitialized(w http.ResponseWriter, r *http.Request, msg mcp.Message) {
	session, err := s.requireSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session.Initialized = true
	s.sessions.put(session)
	w.Header().Set("MCP-Session-Id", session.ID)
	w.Header().Set("MCP-Protocol-Version", session.ProtocolVersion)
	if mcp.HasID(msg.ID) {
		writeJSON(w, http.StatusOK, mcp.NewResponse(msg.ID, map[string]any{}))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) withSession(w http.ResponseWriter, r *http.Request, msg mcp.Message, fn func(session session)) {
	session, err := s.requireSession(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, mcp.NewErrorResponse(msg.ID, -32001, err.Error()))
		return
	}
	if !session.Initialized {
		writeJSON(w, http.StatusBadRequest, mcp.NewErrorResponse(msg.ID, -32002, "session is not initialized"))
		return
	}
	fn(session)
}

func (s *Server) requireSession(r *http.Request) (session, error) {
	sessionID := r.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		return session{}, errors.New("missing MCP-Session-Id header")
	}
	currentSession, ok := s.sessions.get(sessionID)
	if !ok {
		return session{}, errors.New("unknown session")
	}
	return currentSession, nil
}

func (s *Server) writeResponse(w http.ResponseWriter, session session, payload any) {
	w.Header().Set("MCP-Session-Id", session.ID)
	w.Header().Set("MCP-Protocol-Version", session.ProtocolVersion)
	writeJSON(w, http.StatusOK, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *sessionStore) create(version string) session {
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	session := session{
		ID:              hex.EncodeToString(idBytes),
		ProtocolVersion: version,
		CreatedAt:       time.Now().UTC(),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()
	return session
}

func (s *sessionStore) get(id string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *sessionStore) put(session session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
