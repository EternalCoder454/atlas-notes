// Package mcp is "atlas-notes mcp": a Model Context Protocol server, spoken
// over stdin and stdout, that lets Claude Code work with the vault. It is the
// storage layer with a JSON-RPC front on it, and opens no window.
//
// What it may do is the person's choice, made in Settings and re-read on every
// call (see access.go). Password-protected notes are outside it altogether: it
// never has the password, and it answers about a protected note exactly as it
// would about one that does not exist.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Options are what main hands the server.
type Options struct {
	Version string
	// Trash moves a deleted note or folder somewhere it can be restored from.
	// Without one the server refuses to delete anything: a delete it was asked
	// for must always be undoable.
	Trash func(path string) error

	// In and Out default to stdin and stdout. Tests set them.
	In  io.Reader
	Out io.Writer
}

// protocolVersions are the revisions of the protocol this server speaks,
// newest first. A client asking for one of them gets it; any other gets the
// newest, and decides for itself whether it can carry on.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Run serves until the client closes stdin, and returns the exit code.
func Run(opts Options) int {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	// stdout is the protocol. Anything the storage layer logs goes to stderr,
	// which Claude Code keeps as the server's log.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("atlas-notes mcp: ")

	s := newServer(opts)
	defer s.close()
	r := bufio.NewReaderSize(opts.In, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("reading: %v", err)
				return 1
			}
			return 0
		}
	}
}

// writer is the server's half of the conversation. Replies come from the
// request loop and notifications from the settings watcher, so writes are
// serialized: one message is one line, never two interleaved.
type writer struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *writer) send(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("encoding a reply: %v", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.out.Write(append(data, '\n')); err != nil {
		log.Printf("writing a reply: %v", err)
	}
}

// handleLine answers one message, or each message of a batch.
func (s *server) handleLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) > 0 && line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil || len(batch) == 0 {
			s.w.send(response{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{codeParse, "not a JSON-RPC message"}})
			return
		}
		var out []response
		for _, m := range batch {
			if r := s.handleMessage(m); r != nil {
				out = append(out, *r)
			}
		}
		if len(out) > 0 {
			s.w.send(out)
		}
		return
	}
	if r := s.handleMessage(line); r != nil {
		s.w.send(*r)
	}
}

// handleMessage answers one request. A notification, which has no id, gets
// no reply, and neither does a reply from the client to something it was not
// asked.
func (s *server) handleMessage(raw []byte) *response {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{codeParse, "not a JSON-RPC message"}}
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.Method == "" {
		// A reply from the client, or a notification without a method. The
		// server asks the client nothing, so neither is answered.
		return nil
	}
	result, rerr := s.dispatch(req.Method, req.Params)
	if isNotification {
		return nil
	}
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		if result == nil {
			result = struct{}{}
		}
		resp.Result = result
	}
	return resp
}

func (s *server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(params, &p)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": true},
			},
			"serverInfo": map[string]any{
				"name":    "atlas-notes",
				"title":   "Atlas Notes",
				"version": s.opts.Version,
			},
			"instructions": instructions,
		}, nil
	case "notifications/initialized":
		s.startWatching()
		return nil, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": s.listTools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "tools/call needs a name and arguments"}
		}
		t := toolNamed(p.Name)
		if t == nil {
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf("there is no tool called %q", p.Name)}
		}
		return s.call(t, p.Arguments), nil
	}
	if len(method) > 14 && method[:14] == "notifications/" {
		return nil, nil
	}
	return nil, &rpcError{codeMethodNotFound, fmt.Sprintf("%s is not supported", method)}
}

// toolResult is a tools/call result: text for the model, and whether it is a
// failure the model should read and act on.
type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(text string) toolResult {
	return toolResult{Content: []textContent{{Type: "text", Text: text}}}
}

func errorResult(err error) toolResult {
	r := textResult(err.Error())
	r.IsError = true
	return r
}

// instructions are given to the model when it connects: how Atlas Notes
// names and writes things, so that what it creates looks like the person's own
// notes.
const instructions = `Atlas Notes is the person's local Markdown notes app. These tools work on their vault.

Rules that matter most:
- Password-protected notes are not available. They never appear in lists or searches and cannot be read, changed, moved or deleted. If the person asks about one, tell them to open it in Atlas Notes.
- What you may do is set in Atlas Notes, under Settings, Claude Code. If a tool says something is not allowed, tell the person which setting to turn on rather than trying another way.
- Every change is kept in the note's Version History, so the person can undo it.

Working economically:
- Notes are named by path without an extension: "Spec", "Work/Spec", "Daily/2026-10-01".
- For part of a long note, use read_note with outline (headings, line numbers) or heading (one section); paths reads several notes at once. search_notes shows matching lines.
- To change a note, use edit_note with the exact text to replace. Several changes go in one call as the edits array; they apply in order, all or none. Its answer shows the changed lines, so do not read the note again. A miss shows the lines as stored.
- append_to_note adds to the end of a note, or of one section with heading. write_note replaces a whole note; avoid it for small changes.
- To bring an existing file into Atlas Notes, use import_file, not read and create_note.

How notes are written:
- Plain Markdown, usually starting with a "# Title" heading.
- [[Other note]] links to a note by its path; [[Other note|label]] shows a different label.
- #tag anywhere in the text tags the note.
- Checklists use "- [ ] item" and "- [x] done". An item can carry a priority and due date in a trailing comment: "- [ ] Send the report <!-- priority:high due:2026-10-15 -->" (priority high, medium or low).
- Daily notes live in "Daily/YYYY-MM-DD". Notes in "Templates/" are templates for new notes.`
