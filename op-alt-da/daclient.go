package altda

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is returned when the server could not find the data.
var ErrNotFound = errors.New("not found")

// ErrInvalidInput is returned when the data is not valid for posting to the DA service.
var ErrInvalidInput = errors.New("invalid input")

// DAClient stores batcher frames in the DA service and reads them back by commitment.
type DAClient struct {
	url        string
	getTimeout time.Duration
	putTimeout time.Duration
}

// GetInput returns the data behind the commitment, checked against it. Derivation waits on
// ErrNotFound until the service serves the data.
func (c *DAClient) GetInput(ctx context.Context, comm Commitment) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/get/0x%x", c.url, comm.Encode()), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	resp, err := (&http.Client{Timeout: c.getTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get preimage: %v", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := comm.Verify(data); err != nil {
		return nil, err
	}
	return data, nil
}

// SetInput stores the data and returns its commitment.
func (c *DAClient) SetInput(ctx context.Context, data []byte) (Commitment, error) {
	if len(data) == 0 {
		return nil, ErrInvalidInput
	}
	comm := NewCommitment(data)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/put/0x%x", c.url, comm.Encode()), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := (&http.Client{Timeout: c.putTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to store preimage: %v", resp.StatusCode)
	}
	return comm, nil
}
