package provider

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"whirlwind/internal/execution"
)

func startOfflineACPRPC(ctx context.Context, cancel context.CancelFunc, command []string, profile execution.Profile, handler Handler, observers ...func(string, any)) (*RPC, error) {
	conn, err := profile.OpenOfflineACP(ctx, command)
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, output := io.Pipe()
	stderr, diagnostics := io.Pipe()
	go func() {
		err := copyOfflineACPFrames(conn, output, diagnostics)
		_ = output.CloseWithError(err)
		_ = diagnostics.CloseWithError(err)
	}()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
		_ = output.Close()
		_ = diagnostics.Close()
	}()
	drain := func() (int, func(), error) {
		code, err := conn.VerifyCleanup()
		if err == nil && code != 0 {
			err = fmt.Errorf("offline ACP container exited with status %d", code)
		}
		return code, nil, err
	}
	// The fixture may emit ACP output. It cannot execute host callbacks.
	pipes := processPipes{input: conn, output: stdout, stderr: stderr}
	rpc := startRPCTransport(ctx, cancel, pipes, offlineACPNotifications(handler), nil, drain, observers...)
	rpc.offlineWrites = conn.FinishWrites
	return rpc, nil
}

func offlineACPNotifications(handle Handler) Handler {
	return func(ctx context.Context, message Message) (any, error) {
		if len(message.ID) != 0 {
			if message.Method == "whirlwind/offline_finalize" && handle != nil {
				return handle(ctx, message)
			}
			return nil, errors.New("offline ACP cannot request host callbacks")
		}
		if handle == nil || message.Method != "session/update" {
			return nil, nil
		}
		var params struct {
			Update struct{ SessionUpdate string }
		}
		if json.Unmarshal(message.Params, &params) != nil || params.Update.SessionUpdate != "agent_message_chunk" {
			return nil, nil
		}
		return handle(ctx, message)
	}
}

func copyOfflineACPFrames(stream io.Reader, stdout, stderr io.Writer) error {
	var header [8]byte
	for {
		if _, err := io.ReadFull(stream, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[4:])
		if header[1] != 0 || header[2] != 0 || header[3] != 0 || size > 4<<20 {
			return errors.New("invalid offline ACP Docker stream frame")
		}
		var destination io.Writer
		switch header[0] {
		case 1:
			destination = stdout
		case 2:
			destination = stderr
		default:
			return errors.New("unexpected offline ACP Docker stream channel")
		}
		if _, err := io.CopyN(destination, stream, int64(size)); err != nil {
			return err
		}
	}
}
