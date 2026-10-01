package services

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestRedisSnapshotStoreCommandsHonorContextDeadline(t *testing.T) {
	tests := []struct {
		name    string
		command string
		call    func(RedisSnapshotStore, context.Context) error
	}{
		{
			name:    "load",
			command: "GET",
			call: func(store RedisSnapshotStore, ctx context.Context) error {
				_, err := store.Load(ctx)
				return err
			},
		},
		{
			name:    "save",
			command: "SET",
			call: func(store RedisSnapshotStore, ctx context.Context) error {
				return store.Save(ctx, sampleSnapshot(time.Now()), time.Hour)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, started := newBlockedRedisSnapshotClient(t)
			store := RedisSnapshotStore{Client: client, Key: "snapshot:test"}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- test.call(store, ctx) }()

			select {
			case command := <-started:
				if command != test.command {
					t.Fatalf("Redis command = %s, want %s", command, test.command)
				}
			case <-time.After(time.Second):
				t.Fatal("Redis command did not start before the test deadline")
			}

			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("store operation error = %v, want context.DeadlineExceeded", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("store operation did not stop after its context deadline")
			}
		})
	}
}

func newBlockedRedisSnapshotClient(t *testing.T) (*redis.Client, <-chan string) {
	t.Helper()

	started := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:6379",
		MaxRetries:  -1,
		ReadTimeout: 8 * time.Second,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				defer serverConn.Close()
				args, err := readRESPCommand(bufio.NewReader(serverConn))
				if err != nil {
					return
				}
				select {
				case started <- strings.ToUpper(args[0]):
				default:
				}
				<-release
			}()
			return clientConn, nil
		},
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = client.Close()
	})
	return client, started
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[0] != '*' {
		return nil, errors.New("invalid RESP array header")
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 {
		return nil, errors.New("invalid RESP array length")
	}

	args := make([]string, count)
	for i := range args {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) < 3 || line[0] != '$' {
			return nil, errors.New("invalid RESP bulk string header")
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 {
			return nil, errors.New("invalid RESP bulk string length")
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		args[i] = string(value[:length])
	}
	return args, nil
}
