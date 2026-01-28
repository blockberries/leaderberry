package wal

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileWALBasic(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	// Start WAL
	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Write some messages
	msg1 := &Message{
		Type:   MsgTypeProposal,
		Height: 1,
		Round:  0,
	}
	if err := wal.Write(msg1); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	msg2 := &Message{
		Type:   MsgTypeVote,
		Height: 1,
		Round:  0,
	}
	if err := wal.Write(msg2); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	// Stop WAL
	if err := wal.Stop(); err != nil {
		t.Fatalf("failed to stop WAL: %v", err)
	}

	// Verify segment file exists (now using segmented format: wal-00000)
	walPath := filepath.Join(dir, "wal-00000")
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Error("WAL segment file should exist")
	}
}

func TestFileWALWriteSync(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer wal.Stop()

	// WriteSync should flush immediately
	msg := &Message{
		Type:   MsgTypeEndHeight,
		Height: 1,
	}
	if err := wal.WriteSync(msg); err != nil {
		t.Fatalf("failed to write sync message: %v", err)
	}
}

func TestFileWALReadWrite(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Write messages
	messages := []*Message{
		{Type: MsgTypeProposal, Height: 1, Round: 0},
		{Type: MsgTypeVote, Height: 1, Round: 0},
		{Type: MsgTypeEndHeight, Height: 1},
		{Type: MsgTypeProposal, Height: 2, Round: 0},
	}

	for _, msg := range messages {
		if err := wal.Write(msg); err != nil {
			t.Fatalf("failed to write message: %v", err)
		}
	}

	if err := wal.Stop(); err != nil {
		t.Fatalf("failed to stop WAL: %v", err)
	}

	// Read messages back
	reader, err := OpenWALForReading(dir)
	if err != nil {
		t.Fatalf("failed to open WAL for reading: %v", err)
	}
	defer reader.Close()

	readMessages := make([]*Message, 0)
	for {
		msg, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read message: %v", err)
		}
		readMessages = append(readMessages, msg)
	}

	if len(readMessages) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(readMessages))
	}

	for i, msg := range messages {
		if readMessages[i].Type != msg.Type {
			t.Errorf("message %d: expected type %v, got %v", i, msg.Type, readMessages[i].Type)
		}
		if readMessages[i].Height != msg.Height {
			t.Errorf("message %d: expected height %d, got %d", i, msg.Height, readMessages[i].Height)
		}
		if readMessages[i].Round != msg.Round {
			t.Errorf("message %d: expected round %d, got %d", i, msg.Round, readMessages[i].Round)
		}
	}
}

func TestFileWALSearchForEndHeight(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Write messages for height 1
	wal.Write(&Message{Type: MsgTypeProposal, Height: 1, Round: 0})
	wal.Write(&Message{Type: MsgTypeVote, Height: 1, Round: 0})
	wal.Write(&Message{Type: MsgTypeEndHeight, Height: 1})

	// Write messages for height 2
	wal.Write(&Message{Type: MsgTypeProposal, Height: 2, Round: 0})
	wal.Write(&Message{Type: MsgTypeVote, Height: 2, Round: 0})
	wal.Write(&Message{Type: MsgTypeEndHeight, Height: 2})

	// Search for end of height 1
	reader, found, err := wal.SearchForEndHeight(1)
	if err != nil {
		t.Fatalf("failed to search for end height: %v", err)
	}
	if !found {
		t.Error("expected to find end height 1")
	}
	if reader != nil {
		reader.Close()
	}

	// Search for end of height 2
	reader, found, err = wal.SearchForEndHeight(2)
	if err != nil {
		t.Fatalf("failed to search for end height: %v", err)
	}
	if !found {
		t.Error("expected to find end height 2")
	}
	if reader != nil {
		reader.Close()
	}

	// Search for non-existent height
	reader, found, err = wal.SearchForEndHeight(99)
	if err != nil {
		t.Fatalf("failed to search for end height: %v", err)
	}
	if found {
		t.Error("should not find end height 99")
	}
	if reader != nil {
		reader.Close()
	}

	if err := wal.Stop(); err != nil {
		t.Fatalf("failed to stop WAL: %v", err)
	}
}

func TestFileWALWriteBeforeStart(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	// Writing before start should fail
	msg := &Message{Type: MsgTypeProposal, Height: 1}
	err = wal.Write(msg)
	if err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed, got %v", err)
	}
}

func TestFileWALDoubleStart(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	// First start
	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Second start should be a no-op
	if err := wal.Start(); err != nil {
		t.Errorf("double start should be a no-op, got: %v", err)
	}

	if err := wal.Stop(); err != nil {
		t.Fatalf("failed to stop WAL: %v", err)
	}
}

func TestFileWALDoubleStop(t *testing.T) {
	dir := t.TempDir()

	wal, err := NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	if err := wal.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// First stop
	if err := wal.Stop(); err != nil {
		t.Fatalf("failed to stop WAL: %v", err)
	}

	// Second stop should be a no-op
	if err := wal.Stop(); err != nil {
		t.Errorf("double stop should be a no-op, got: %v", err)
	}
}

func TestOpenWALNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := OpenWALForReading(dir)
	if err != ErrWALNotFound {
		t.Errorf("expected ErrWALNotFound, got %v", err)
	}
}

func TestMessageTypes(t *testing.T) {
	// Test all message types
	msgTypes := []MessageType{
		MsgTypeProposal,
		MsgTypeVote,
		MsgTypeEndHeight,
		MsgTypeCommit,
		MsgTypeState,
		MsgTypeTimeout,
	}

	dir := t.TempDir()
	wal, _ := NewFileWAL(dir)
	wal.Start()
	defer wal.Stop()

	for _, msgType := range msgTypes {
		msg := &Message{
			Type:   msgType,
			Height: 1,
			Round:  0,
		}
		if err := wal.Write(msg); err != nil {
			t.Errorf("failed to write message type %v: %v", msgType, err)
		}
	}
}
