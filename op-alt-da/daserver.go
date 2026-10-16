package altda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum-optimism/optimism/op-service/log"
)

type KVStore interface {
	// Get returns the value stored under key, or ErrNotFound.
	Get(ctx context.Context, key []byte) ([]byte, error)
	Put(ctx context.Context, key []byte, value []byte) error
}

// DAServer stores batcher frames under their encoded commitment. Anyone can read; writes are
// only accepted when the data matches the commitment.
type DAServer struct {
	log        log.Logger
	endpoint   string
	store      KVStore
	httpServer *http.Server
	listener   net.Listener
}

func NewDAServer(host string, port int, store KVStore, log log.Logger) *DAServer {
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	return &DAServer{
		log:        log,
		endpoint:   endpoint,
		store:      store,
		httpServer: &http.Server{Addr: endpoint},
	}
}

func (d *DAServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /get/{comm}", d.HandleGet)
	mux.HandleFunc("POST /put/{comm}", d.HandlePut)
	d.httpServer.Handler = mux

	listener, err := net.Listen("tcp", d.endpoint)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	d.listener = listener
	d.endpoint = listener.Addr().String()
	go func() {
		if err := d.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.Error("DA server stopped", "err", err)
		}
	}()
	return nil
}

func (d *DAServer) commitment(w http.ResponseWriter, r *http.Request) (Commitment, bool) {
	encoded, err := hexutil.Decode(path.Base(r.URL.Path))
	if err == nil {
		var comm Commitment
		if comm, err = DecodeCommitment(encoded); err == nil {
			return comm, true
		}
	}
	d.log.Warn("Invalid commitment", "url", r.URL, "err", err)
	w.WriteHeader(http.StatusBadRequest)
	return nil, false
}

func (d *DAServer) HandleGet(w http.ResponseWriter, r *http.Request) {
	comm, ok := d.commitment(w, r)
	if !ok {
		return
	}
	data, err := d.store.Get(r.Context(), comm.Encode())
	if errors.Is(err, ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	} else if err != nil {
		d.log.Error("Failed to read data", "comm", comm, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		d.log.Error("Failed to write response", "comm", comm, "err", err)
	}
}

func (d *DAServer) HandlePut(w http.ResponseWriter, r *http.Request) {
	comm, ok := d.commitment(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil || comm.Verify(data) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := d.store.Put(r.Context(), comm.Encode(), data); err != nil {
		d.log.Error("Failed to store data", "comm", comm, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	d.log.Info("Stored data", "comm", comm, "len", len(data))
}

func (d *DAServer) HttpEndpoint() string {
	return fmt.Sprintf("http://%s", d.listener.Addr().String())
}

func (d *DAServer) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.httpServer.Shutdown(ctx)
}
