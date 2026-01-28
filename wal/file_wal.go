package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// WAL file settings
	walFilePerm       = 0600
	walDirPerm        = 0700
	maxMsgSize        = 10 * 1024 * 1024 // 10MB max message size
	defaultBufSize    = 64 * 1024        // 64KB buffer
	defaultMaxSegSize = 64 * 1024 * 1024 // 64MB default segment size

	// L2: Default pool buffer size for decoder
	defaultPoolBufSize = 4096
)

// L2: Byte pool to reduce GC pressure in WAL decoder
// Buffers are reused for reading message data, then copied for the final Message.
var decoderPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 0, defaultPoolBufSize)
		return &buf
	},
}

// FileWAL is a file-based WAL implementation
type FileWAL struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	buf  *bufio.Writer
	enc  *encoder

	group        *Group
	started      bool
	segmentIndex int   // Current segment index
	segmentSize  int64 // Current segment size in bytes
	maxSegSize   int64 // Maximum segment size before rotation

	// Index for O(1) height lookup
	// Maps height -> segment index where EndHeight message was written
	heightIndex map[int64]int
}

// NewFileWAL creates a new file-based WAL
func NewFileWAL(dir string) (*FileWAL, error) {
	return NewFileWALWithOptions(dir, defaultMaxSegSize)
}

// NewFileWALWithOptions creates a new file-based WAL with custom max segment size
func NewFileWALWithOptions(dir string, maxSegSize int64) (*FileWAL, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, walDirPerm); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	if maxSegSize <= 0 {
		maxSegSize = defaultMaxSegSize
	}

	return &FileWAL{
		dir:        dir,
		maxSegSize: maxSegSize,
		group: &Group{
			Dir:     dir,
			Prefix:  "wal",
			MaxSize: maxSegSize,
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

	// Initialize height index
	w.heightIndex = make(map[int64]int)

	// Find existing segments and determine the highest index
	// H6: Check for migration errors
	idx, err := w.findHighestSegmentIndex()
	if err != nil {
		return fmt.Errorf("failed to find WAL segments: %w", err)
	}
	w.segmentIndex = idx
	w.group.MinIndex = w.findLowestSegmentIndex()
	w.group.MaxIndex = w.segmentIndex

	// Build index from existing segments
	if err := w.buildIndex(); err != nil {
		return fmt.Errorf("failed to build WAL index: %w", err)
	}

	// Open or create current segment
	if err := w.openSegment(w.segmentIndex); err != nil {
		return err
	}

	w.started = true
	return nil
}

// buildIndex scans all segments and builds the height -> segment index
func (w *FileWAL) buildIndex() error {
	for idx := w.group.MinIndex; idx <= w.group.MaxIndex; idx++ {
		path := w.segmentPath(idx)
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		dec := newDecoder(bufio.NewReader(file))
		for {
			msg, err := dec.Decode()
			if err == io.EOF {
				break
			}
			if err != nil {
				// Corrupted segment - stop indexing this segment
				break
			}

			if msg.Type == MsgTypeEndHeight {
				w.heightIndex[msg.Height] = idx
			}
		}
		file.Close()
	}
	return nil
}

// findHighestSegmentIndex finds the highest segment index in the WAL directory
// H6: Returns error if legacy migration fails (prevents silent data loss)
func (w *FileWAL) findHighestSegmentIndex() (int, error) {
	// Check for legacy "wal" file first
	legacyPath := filepath.Join(w.dir, "wal")
	if _, err := os.Stat(legacyPath); err == nil {
		// Migrate legacy file to segment-0
		newPath := w.segmentPath(0)
		if err := os.Rename(legacyPath, newPath); err != nil {
			// H6: Return error on migration failure instead of silently continuing
			return 0, fmt.Errorf("failed to migrate legacy WAL file: %w", err)
		}
		log.Printf("INFO: migrated legacy WAL to segmented format")
		return 0, nil
	}

	// Find highest numbered segment
	highest := -1
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return 0, nil // Directory doesn't exist yet, that's fine
	}

	for _, entry := range entries {
		var idx int
		if n, _ := fmt.Sscanf(entry.Name(), "wal-%05d", &idx); n == 1 {
			if idx > highest {
				highest = idx
			}
		}
	}

	if highest < 0 {
		return 0, nil
	}
	return highest, nil
}

// findLowestSegmentIndex finds the lowest segment index in the WAL directory
func (w *FileWAL) findLowestSegmentIndex() int {
	lowest := -1
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		var idx int
		if n, _ := fmt.Sscanf(entry.Name(), "wal-%05d", &idx); n == 1 {
			if lowest < 0 || idx < lowest {
				lowest = idx
			}
		}
	}

	if lowest < 0 {
		return 0
	}
	return lowest
}

// segmentPath returns the file path for a segment index
func (w *FileWAL) segmentPath(index int) string {
	return filepath.Join(w.dir, fmt.Sprintf("wal-%05d", index))
}

// openSegment opens a segment file for writing
func (w *FileWAL) openSegment(index int) error {
	path := w.segmentPath(index)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, walFilePerm)
	if err != nil {
		return fmt.Errorf("failed to open WAL segment %d: %w", index, err)
	}

	// Get current file size
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to stat WAL segment: %w", err)
	}

	w.file = file
	w.buf = bufio.NewWriterSize(file, defaultBufSize)
	w.enc = newEncoder(w.buf)
	w.segmentSize = info.Size()

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

	// Check if rotation is needed before writing
	if w.segmentSize >= w.maxSegSize {
		if err := w.rotate(); err != nil {
			return fmt.Errorf("failed to rotate WAL: %w", err)
		}
	}

	n, err := w.enc.Encode(msg)
	if err != nil {
		return err
	}
	w.segmentSize += int64(n)

	// Update height index for EndHeight messages
	if msg.Type == MsgTypeEndHeight {
		w.heightIndex[msg.Height] = w.segmentIndex
	}

	return nil
}

// rotate closes the current segment and opens a new one
func (w *FileWAL) rotate() error {
	// Flush and sync current segment
	if err := w.flushAndSync(); err != nil {
		return err
	}

	// Close current file
	if err := w.file.Close(); err != nil {
		return err
	}

	// Increment segment index
	w.segmentIndex++
	w.group.MaxIndex = w.segmentIndex

	// Open new segment
	return w.openSegment(w.segmentIndex)
}

// WriteSync writes a message and syncs to disk
func (w *FileWAL) WriteSync(msg *Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return ErrWALClosed
	}

	// Check if rotation is needed before writing
	if w.segmentSize >= w.maxSegSize {
		if err := w.rotate(); err != nil {
			return fmt.Errorf("failed to rotate WAL: %w", err)
		}
	}

	n, err := w.enc.Encode(msg)
	if err != nil {
		return err
	}
	w.segmentSize += int64(n)

	// Update height index for EndHeight messages
	if msg.Type == MsgTypeEndHeight {
		w.heightIndex[msg.Height] = w.segmentIndex
	}

	return w.flushAndSync()
}

// FlushAndSync flushes the buffer and syncs to disk.
// Safe for concurrent use.
func (w *FileWAL) FlushAndSync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return ErrWALClosed
	}

	return w.flushAndSync()
}

// flushAndSync is the internal version that assumes lock is held
func (w *FileWAL) flushAndSync() error {
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// SearchForEndHeight searches for the end of a height in the WAL.
// Uses the height index for O(1) segment lookup when available.
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

	// Check height index for O(1) lookup
	if segIdx, ok := w.heightIndex[height]; ok {
		// Found in index - search only this segment
		reader, found, err := w.searchSegmentForEndHeight(segIdx, height)
		if err != nil {
			return nil, false, err
		}
		if found {
			return reader, true, nil
		}
		// Index was stale - fall through to full scan
	}

	// Fallback: search through all segments from oldest to newest
	for idx := w.group.MinIndex; idx <= w.group.MaxIndex; idx++ {
		reader, found, err := w.searchSegmentForEndHeight(idx, height)
		if err != nil {
			return nil, false, err
		}
		if found {
			// Update index for future lookups
			w.heightIndex[height] = idx
			return reader, true, nil
		}
	}

	return nil, false, nil
}

// searchSegmentForEndHeight searches a single segment for an EndHeight message
func (w *FileWAL) searchSegmentForEndHeight(segmentIndex int, height int64) (Reader, bool, error) {
	path := w.segmentPath(segmentIndex)
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

// Checkpoint deletes WAL segments that only contain heights <= checkpointHeight.
// This should be called after the state has been safely persisted up to checkpointHeight.
func (w *FileWAL) Checkpoint(checkpointHeight int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return ErrWALClosed
	}

	// Find segments that can be deleted
	// A segment can be deleted if all its messages have height <= checkpointHeight
	segmentsToDelete := []int{}

	for idx := w.group.MinIndex; idx < w.group.MaxIndex; idx++ { // Never delete current segment
		canDelete, err := w.canDeleteSegment(idx, checkpointHeight)
		if err != nil {
			// If we can't read the segment, skip it
			continue
		}
		if canDelete {
			segmentsToDelete = append(segmentsToDelete, idx)
		} else {
			// Stop at first segment we can't delete
			break
		}
	}

	// Delete segments and clean up index
	for _, idx := range segmentsToDelete {
		path := w.segmentPath(idx)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete segment %d: %w", idx, err)
		}

		// Remove heights from index that pointed to this segment
		for h, segIdx := range w.heightIndex {
			if segIdx == idx {
				delete(w.heightIndex, h)
			}
		}
	}

	// Update MinIndex
	if len(segmentsToDelete) > 0 {
		w.group.MinIndex = segmentsToDelete[len(segmentsToDelete)-1] + 1
	}

	return nil
}

// canDeleteSegment checks if a segment can be deleted (all heights <= checkpointHeight)
func (w *FileWAL) canDeleteSegment(segmentIndex int, checkpointHeight int64) (bool, error) {
	path := w.segmentPath(segmentIndex)
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	dec := newDecoder(bufio.NewReader(file))
	maxHeight := int64(0)

	for {
		msg, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Corrupted segment - don't delete
			return false, err
		}
		if msg.Height > maxHeight {
			maxHeight = msg.Height
		}
	}

	return maxHeight <= checkpointHeight, nil
}

// SegmentCount returns the number of segments
func (w *FileWAL) SegmentCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.group.MaxIndex - w.group.MinIndex + 1
}

// CurrentSegmentSize returns the approximate size of the current segment
func (w *FileWAL) CurrentSegmentSize() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.segmentSize
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

// Encode writes a message to the WAL and returns the number of bytes written.
func (e *encoder) Encode(msg *Message) (int, error) {
	// Serialize message
	data, err := msg.MarshalCramberry()
	if err != nil {
		return 0, err
	}

	// Calculate CRC32 checksum
	checksum := crc32.ChecksumIEEE(data)

	// Write length prefix (4 bytes, big endian)
	binary.BigEndian.PutUint32(e.buf[:4], uint32(len(data)))
	if _, err := e.w.Write(e.buf[:4]); err != nil {
		return 0, err
	}

	// Write data
	if _, err := e.w.Write(data); err != nil {
		return 0, err
	}

	// Write CRC32 checksum (4 bytes, big endian)
	binary.BigEndian.PutUint32(e.buf[:4], checksum)
	if _, err := e.w.Write(e.buf[:4]); err != nil {
		return 0, err
	}

	// Total bytes: 4 (length) + data + 4 (crc)
	return 4 + len(data) + 4, nil
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

	// L2: Get buffer from pool to reduce GC pressure
	poolBufPtr := decoderPool.Get().(*[]byte)
	poolBuf := *poolBufPtr

	// Ensure buffer is large enough
	if cap(poolBuf) < int(length) {
		// Need a larger buffer - allocate new one
		poolBuf = make([]byte, length)
	} else {
		poolBuf = poolBuf[:length]
	}

	// Read data into pooled buffer
	if _, err := io.ReadFull(d.r, poolBuf); err != nil {
		// Return buffer to pool before returning error
		*poolBufPtr = poolBuf[:0]
		decoderPool.Put(poolBufPtr)
		return nil, err
	}

	// Read and verify CRC32 checksum
	if _, err := io.ReadFull(d.r, d.buf[:4]); err != nil {
		*poolBufPtr = poolBuf[:0]
		decoderPool.Put(poolBufPtr)
		return nil, err
	}
	expectedCRC := binary.BigEndian.Uint32(d.buf[:4])
	actualCRC := crc32.ChecksumIEEE(poolBuf)
	if expectedCRC != actualCRC {
		*poolBufPtr = poolBuf[:0]
		decoderPool.Put(poolBufPtr)
		return nil, fmt.Errorf("%w: CRC mismatch (expected %08x, got %08x)", ErrWALCorrupted, expectedCRC, actualCRC)
	}

	// Make a copy for the message (message takes ownership)
	data := make([]byte, length)
	copy(data, poolBuf)

	// Return buffer to pool
	*poolBufPtr = poolBuf[:0]
	decoderPool.Put(poolBufPtr)

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

// OpenWALForReading opens a WAL for reading from the beginning.
// Handles both legacy single-file WAL and segmented WAL.
func OpenWALForReading(dir string) (Reader, error) {
	// First, check for legacy "wal" file
	legacyPath := filepath.Join(dir, "wal")
	if _, err := os.Stat(legacyPath); err == nil {
		file, err := os.Open(legacyPath)
		if err != nil {
			return nil, err
		}
		return &fileReader{
			file: file,
			dec:  newDecoder(bufio.NewReader(file)),
		}, nil
	}

	// Find all segment files
	segments := findSegments(dir)
	if len(segments) == 0 {
		return nil, ErrWALNotFound
	}

	return &multiSegmentReader{
		dir:      dir,
		segments: segments,
		current:  -1, // Will be incremented to 0 on first read
	}, nil
}

// findSegments finds all WAL segment files in a directory and returns their indices
func findSegments(dir string) []int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var segments []int
	for _, entry := range entries {
		var idx int
		if n, _ := fmt.Sscanf(entry.Name(), "wal-%05d", &idx); n == 1 {
			segments = append(segments, idx)
		}
	}

	// L3: Sort segments by index using standard library
	sort.Ints(segments)

	return segments
}

// multiSegmentReader reads through multiple WAL segments
type multiSegmentReader struct {
	dir      string
	segments []int
	current  int
	reader   *fileReader
}

func (r *multiSegmentReader) Read() (*Message, error) {
	for {
		// If no current reader, open next segment
		if r.reader == nil {
			r.current++
			if r.current >= len(r.segments) {
				return nil, io.EOF
			}

			path := filepath.Join(r.dir, fmt.Sprintf("wal-%05d", r.segments[r.current]))
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			r.reader = &fileReader{
				file: file,
				dec:  newDecoder(bufio.NewReader(file)),
			}
		}

		// Try to read from current segment
		msg, err := r.reader.Read()
		if err == io.EOF {
			// End of this segment, move to next
			r.reader.Close()
			r.reader = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		return msg, nil
	}
}

func (r *multiSegmentReader) Close() error {
	if r.reader != nil {
		return r.reader.Close()
	}
	return nil
}

var _ Reader = (*multiSegmentReader)(nil)
