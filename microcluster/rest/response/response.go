package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/log"
)

// Init registers smart error mappings.
func Init(smartErrors map[int][]error) {
	for code, additionalErrors := range smartErrors {
		existingErrs, ok := httpResponseErrors[code]
		if ok {
			httpResponseErrors[code] = append(existingErrs, additionalErrors...)
			continue
		}

		httpResponseErrors[code] = additionalErrors
	}
}

// Response represents an API response.
type Response interface {
	Render(w http.ResponseWriter, r *http.Request) error
	String() string
}

// Sync response.
type syncResponse struct {
	success  bool
	metadata any
}

// EmptySyncResponse represents an empty success response.
var EmptySyncResponse = &syncResponse{success: true, metadata: make(map[string]any)}

// SyncResponse returns a new syncResponse with the success and metadata fields set.
func SyncResponse(success bool, metadata any) Response {
	return &syncResponse{success: success, metadata: metadata}
}

func (r *syncResponse) Render(w http.ResponseWriter, req *http.Request) error {
	if w.Header().Get("Connection") != "keep-alive" {
		w.WriteHeader(http.StatusOK)
	}

	status := api.Success
	if !r.success {
		status = api.Failure

		// If metadata is an error, consider the response a SmartError.
		err, ok := r.metadata.(error)
		if ok {
			return SmartError(err).Render(w, req)
		}
	}

	resp := api.ResponseRaw{
		Type:       api.SyncResponse,
		Status:     status.String(),
		StatusCode: int(status),
		Metadata:   r.metadata,
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(resp)
}

func (r *syncResponse) String() string {
	if r.success {
		return "success"
	}

	return "failure"
}

// Error response.
type errorResponse struct {
	code int
	err  error
}

// BadRequest returns a bad request response (400) with the given error.
func BadRequest(err error) Response {
	return &errorResponse{http.StatusBadRequest, err}
}

// InternalError returns an internal error response (500) with the given error.
func InternalError(err error) Response {
	return &errorResponse{http.StatusInternalServerError, err}
}

// Forbidden returns a forbidden response (403) with the given error.
func Forbidden(err error) Response {
	return &errorResponse{http.StatusForbidden, err}
}

// NotFound returns a not found response (404) with the given error.
func NotFound(err error) Response {
	return &errorResponse{http.StatusNotFound, err}
}

// NotImplemented returns a not implemented response (501) with the given error.
func NotImplemented(err error) Response {
	return &errorResponse{http.StatusNotImplemented, err}
}

// Unavailable returns an unavailable response (503) with the given error.
func Unavailable(err error) Response {
	return &errorResponse{http.StatusServiceUnavailable, err}
}

func (r *errorResponse) Render(w http.ResponseWriter, req *http.Request) error {
	buf := &bytes.Buffer{}
	resp := api.ResponseRaw{
		Type:  api.ErrorResponse,
		Error: r.String(),
		Code:  r.code,
	}

	err := json.NewEncoder(buf).Encode(resp)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if w.Header().Get("Connection") != "keep-alive" {
		w.WriteHeader(r.code)
	}

	_, err = w.Write(buf.Bytes())
	return err
}

func (r *errorResponse) String() string {
	if r.err != nil {
		return r.err.Error()
	}

	return http.StatusText(r.code)
}

// Manual response.
type manualResponse struct {
	hook func(w http.ResponseWriter) error
}

// ManualResponse creates a new manual response.
func ManualResponse(hook func(w http.ResponseWriter) error) Response {
	return &manualResponse{hook: hook}
}

func (r *manualResponse) Render(w http.ResponseWriter, req *http.Request) error {
	return r.hook(w)
}

func (r *manualResponse) String() string {
	return "manual response"
}

// ParseResponse takes an HTTP response, parses it and returns the extracted result.
func ParseResponse(resp *http.Response) (*api.Response, error) {
	decoder := json.NewDecoder(resp.Body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Failed to fetch %q: %q", resp.Request.URL.String(), resp.Status)
		}

		return nil, err
	}

	if response.Type == api.ErrorResponse {
		return nil, api.StatusErrorf(resp.StatusCode, "%s", response.Error)
	}

	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		logger, logErr := log.LoggerFromContext(resp.Request.Context())
		if logErr != nil {
			return nil, err
		}

		logger.Error("Failed to read response body", slog.String("error", err.Error()))
	}

	return &response, nil
}
