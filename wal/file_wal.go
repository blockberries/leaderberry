package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	// WAL file settings
	walFilePerm    = 0600
	walDirPerm     = 0700
	maxMsgSize     = 10 * 1024 * 1024 // 10MB max message size
	defaultBufSize = 64 * 1024        // 64KB buffer
)

// FileWAL is a file-based WAL implementation
type FileWAL struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	buf  *bufio.Writer
	enc  *encoder

	group   *Group
	started bool
}

// NewFileWAL creates a new file-based WAL
func NewFileWAL(dir string) (*FileWAL, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, walDirPerm); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	return &FileWAL{
		dir: dir,
		group: &Group{
			Dir:    dir,
			Prefix: "wal",
		},
	}, nil
}

// Start opens the WAL file for writing
func (w *FileWAL) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.started {
		return nil
	}

	// Open or create WAL file
	path := filepath.Join(w.dir, "wal")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, walFilePerm)
	if err != nil {
		return fmt.Errorf("failed to open WAL file: %w", err)
	}

	w.file = file
	w.buf = bufio.NewWriterSize(file, defaultBufSize)
	w.enc = newEncoder(w.buf)
	w.started = true

	return nil
}

// Stop closes the WAL file
func (w *FileWAL) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return nil
	}

	w.started = false

	// Flush buffer
	if err := w.buf.Flush(); err != nil {
		return err
	}

	// Sync and close file
	if err := w.file.Sync(); err != nil {
		return err
	}

	return w.file.Close()
}

// Write writes a message to the WAL (buffered)
func (w *FileWAL) Write(msg *Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return ErrWALClosed
	}

	return w.enc.Encode(msg)
}

// WriteSync writes a message and syncs to disk
func (w *FileWAL) WriteSync(msg *Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return ErrWALClosed
	}

	if err := w.enc.Encode(msg); err != nil {
		return err
	}

	return w.FlushAndSync()
}

// FlushAndSync flushes the buffer and syncs to disk
func (w *FileWAL) FlushAndSync() error {
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// SearchForEndHeight searches for the end of a height in the WAL
func (w *FileWAL) SearchForEndHeight(height int64) (Reader, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return nil, false, ErrWALClosed
	}

	// Flush any pending writes
	if err := w.buf.Flush(); err != nil {
		return nil, false, err
	}

	// Open file for reading
	path := filepath.Join(w.dir, "wal")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	reader := &fileReader{
		file: file,
		dec:  newDecoder(bufio.NewReader(file)),
	}

	// Search for EndHeight message
	for {
		msg, err := reader.Read()
		if err == io.EOF {
			reader.Close()
			return nil, false, nil
		}
		if err != nil {
			reader.Close()
			return nil, false, err
		}

		if msg.Type == MsgTypeEndHeight && msg.Height == height {
			// Found it - return reader positioned after this message
			return reader, true, nil
		}
	}
}

// Group returns the WAL group
func (w *FileWAL) Group() *Group {
	return w.group
}

// Ensure FileWAL implements WAL
var _ WAL = (*FileWAL)(nil)

// encoder encodes messages to the WAL
type encoder struct {
	w   io.Writer
	buf []byte
}

func newEncoder(w io.Writer) *encoder {
	return &encoder{
		w:   w,
		buf: make([]byte, 8),
	}
}

func (e *encoder) Encode(msg *Message) error {
	// Serialize message
	data, err := msg.MarshalCramberry()
	if err != nil {
		return err
	}

	// Write length prefix (4 bytes, big endian)
	binary.BigEndian.PutUint32(e.buf[:4], uint32(len(data)))
	if _, err := e.w.Write(e.buf[:4]); err != nil {
		return err
	}

	// Write data
	if _, err := e.w.Write(data); err != nil {
		return err
	}

	return nil
}

// decoder decodes messages from the WAL
type decoder struct {
	r   io.Reader
	buf []byte
}

func newDecoder(r io.Reader) *decoder {
	return &decoder{
		r:   r,
		buf: make([]byte, 4),
	}
}

func (d *decoder) Decode() (*Message, error) {
	// Read length prefix
	if _, err := io.ReadFull(d.r, d.buf[:4]); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(d.buf[:4])
	if length > maxMsgSize {
		return nil, ErrWALCorrupted
	}

	// Read data
	data := make([]byte, length)
	if _, err := io.ReadFull(d.r, data); err != nil {
		return nil, err
	}

	// Deserialize message
	msg := &Message{}
	if err := msg.UnmarshalCramberry(data); err != nil {
		return nil, err
	}

	return msg, nil
}

// fileReader reads messages from a WAL file
type fileReader struct {
	file *os.File
	dec  *decoder
}

func (r *fileReader) Read() (*Message, error) {
	return r.dec.Decode()
}

func (r *fileReader) Close() error {
	return r.file.Close()
}

var _ Reader = (*fileReader)(nil)

// OpenWALForReading opens a WAL file for reading from the beginning
func OpenWALForReading(dir string) (Reader, error) {
	path := filepath.Join(dir, "wal")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrWALNotFound
		}
		return nil, err
	}

	return &fileReader{
		file: file,
		dec:  newDecoder(bufio.NewReader(file)),
	}, nil
}
