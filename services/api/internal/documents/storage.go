package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Storage is the private object store for user documents (ADR-008). Keys are
// random UUIDs; no personal data appears in keys.
type Storage interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	Ping(ctx context.Context) error
	// List calls fn for every object under prefix (orphan sweep).
	List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}

// ObjectInfo describes a stored object without its contents.
type ObjectInfo struct {
	Key      string
	Modified time.Time
}

var ErrObjectNotFound = errors.New("object not found")

// S3 implements Storage against any S3-compatible service.
type S3 struct {
	c      *minio.Client
	bucket string
}

type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	UseSSL                                         bool
}

func NewS3(cfg S3Config) (*S3, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return &S3{c: c, bucket: cfg.Bucket}, nil
}

// EnsureBucket creates the bucket if missing. Used in development; production
// buckets are provisioned (private, encrypted) by infrastructure.
func (s *S3) EnsureBucket(ctx context.Context, region string) error {
	exists, err := s.c.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.c.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}
	return nil
}

func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (s *S3) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the listing goroutine if fn returns early
	for obj := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("list objects: %w", obj.Err)
		}
		if err := fn(ObjectInfo{Key: obj.Key, Modified: obj.LastModified}); err != nil {
			return err
		}
	}
	return nil
}

// Ping fails when the store is unreachable or the bucket does not exist
// (uploads would fail), so readiness reflects both.
func (s *S3) Ping(ctx context.Context) error {
	exists, err := s.c.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}

// Memory is an in-process Storage for tests.
type Memory struct {
	mu   sync.Mutex
	objs map[string][]byte
	mods map[string]time.Time
}

func NewMemory() *Memory { return &Memory{objs: map[string][]byte{}, mods: map[string]time.Time{}} }

func (m *Memory) Put(_ context.Context, key string, data []byte, _ string) error {
	return m.PutAt(key, data, time.Now())
}

// PutAt stores an object with an explicit modification time (tests).
func (m *Memory) PutAt(key string, data []byte, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = append([]byte(nil), data...)
	m.mods[key] = at
	return nil
}

func (m *Memory) List(_ context.Context, prefix string, fn func(ObjectInfo) error) error {
	m.mu.Lock()
	var infos []ObjectInfo
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) {
			infos = append(infos, ObjectInfo{Key: k, Modified: m.mods[k]})
		}
	}
	m.mu.Unlock()
	for _, i := range infos {
		if err := fn(i); err != nil {
			return err
		}
	}
	return nil
}

// Has reports whether key is stored (tests).
func (m *Memory) Has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[key]
	return ok
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.objs[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), d...), nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	delete(m.mods, key)
	return nil
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objs)
}
