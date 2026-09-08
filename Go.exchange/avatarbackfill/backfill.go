package avatarbackfill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"Go.exchange/avatarimage"
	"Go.exchange/config"
	"Go.exchange/devdata"
	"Go.exchange/models"
	"Go.exchange/profileavatar"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultBatchSize = 100

var ErrObjectNotFound = errors.New("avatar object not found")

// AvatarObjectInfo is the metadata needed to decide whether a V1 derivative
// can be reused without downloading it again.
type AvatarObjectInfo struct {
	Size        int64
	ContentType string
}

// ObjectStore is deliberately independent from devdata.AvatarObjectStore.
// Backfill needs Get, while post-media mirroring intentionally does not.
type ObjectStore interface {
	Get(ctx context.Context, objectKey string) ([]byte, AvatarObjectInfo, error)
	Stat(ctx context.Context, objectKey string) (AvatarObjectInfo, bool, error)
	Put(ctx context.Context, objectKey string, body []byte, contentType string) error
}

type Options struct {
	Apply     bool
	BatchSize int
}

type Report struct {
	RegularScanned          int
	DevDataScanned          int
	Eligible                int
	AlreadyV1               int
	Empty                   int
	UnsupportedURL          int
	MissingObject           int
	Optimized               int
	WouldUpload             int
	Uploaded                int
	Reused                  int
	DBUpdated               int
	ConcurrentChangeSkipped int
	Failed                  int
}

func (r Report) WriteReport(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "regular_scanned=%d\n", r.RegularScanned)
	fmt.Fprintf(w, "devdata_scanned=%d\n", r.DevDataScanned)
	fmt.Fprintf(w, "eligible=%d\n", r.Eligible)
	fmt.Fprintf(w, "already_v1=%d\n", r.AlreadyV1)
	fmt.Fprintf(w, "empty=%d\n", r.Empty)
	fmt.Fprintf(w, "unsupported_url=%d\n", r.UnsupportedURL)
	fmt.Fprintf(w, "missing_object=%d\n", r.MissingObject)
	fmt.Fprintf(w, "optimized=%d\n", r.Optimized)
	fmt.Fprintf(w, "would_upload=%d\n", r.WouldUpload)
	fmt.Fprintf(w, "uploaded=%d\n", r.Uploaded)
	fmt.Fprintf(w, "reused=%d\n", r.Reused)
	fmt.Fprintf(w, "db_updated=%d\n", r.DBUpdated)
	fmt.Fprintf(w, "concurrent_change_skipped=%d\n", r.ConcurrentChangeSkipped)
	fmt.Fprintf(w, "failed=%d\n", r.Failed)
}

// Run performs a keyset-paginated avatar migration. Per-row failures are
// reported in Report and do not stop later rows.
func Run(ctx context.Context, db *gorm.DB, store ObjectStore, options Options) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return Report{}, errors.New("database is not initialized")
	}
	if store == nil {
		return Report{}, errors.New("avatar object store is not initialized")
	}
	if options.BatchSize == 0 {
		options.BatchSize = DefaultBatchSize
	}
	if options.BatchSize < 1 {
		return Report{}, errors.New("batch size must be at least 1")
	}

	report := Report{}
	if err := runRegularUsers(ctx, db, store, options, &report); err != nil {
		return report, err
	}
	if err := runDevDataAccounts(ctx, db, store, options, &report); err != nil {
		return report, err
	}
	return report, nil
}

type capturedRegularUser struct {
	ID        uint
	AvatarURL string
}

func runRegularUsers(ctx context.Context, db *gorm.DB, store ObjectStore, options Options, report *Report) error {
	var lastID uint
	for {
		var users []models.User
		query := db.WithContext(ctx).
			Where("users.id > ?", lastID).
			Where("NOT EXISTS (SELECT 1 FROM devdata_mirror_accounts AS mirror WHERE mirror.local_user_id = users.id)").
			Order("users.id ASC").
			Limit(options.BatchSize).
			Find(&users)
		if query.Error != nil {
			return fmt.Errorf("scan regular users: %w", query.Error)
		}
		if len(users) == 0 {
			return nil
		}
		for _, user := range users {
			lastID = user.ID
			report.RegularScanned++
			processRegularUser(ctx, db, store, options, capturedRegularUser{ID: user.ID, AvatarURL: user.AvatarURL}, report)
		}
		if len(users) < options.BatchSize {
			return nil
		}
	}
}

func processRegularUser(ctx context.Context, db *gorm.DB, store ObjectStore, options Options, captured capturedRegularUser, report *Report) {
	if strings.TrimSpace(captured.AvatarURL) == "" {
		report.Empty++
		return
	}
	legacyKey, kind, err := profileavatar.ParseUserAvatarURL(captured.AvatarURL, captured.ID)
	if err != nil {
		report.UnsupportedURL++
		return
	}
	if kind == profileavatar.UserAvatarURLV1 {
		report.AlreadyV1++
		return
	}
	report.Eligible++

	body, _, err := store.Get(ctx, legacyKey)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			report.MissingObject++
		}
		report.Failed++
		return
	}
	derivative, err := avatarimage.Optimize(body)
	if err != nil {
		report.Failed++
		return
	}
	report.Optimized++
	v1Key, err := profileavatar.BuildUserV1ObjectKey(captured.ID, derivative.ContentHash, derivative.Extension)
	if err != nil {
		report.Failed++
		return
	}
	needsUpload, ok := inspectTarget(ctx, store, v1Key, derivative, report)
	if !ok {
		return
	}
	if !options.Apply {
		return
	}
	if needsUpload {
		if err := store.Put(ctx, v1Key, derivative.Body, derivative.ContentType); err != nil {
			report.Failed++
			return
		}
		report.Uploaded++
	}
	updated, err := updateRegularUserCAS(ctx, db, captured.ID, captured.AvatarURL, profileavatar.FilesURLPrefix+v1Key)
	if err != nil {
		report.Failed++
		return
	}
	if !updated {
		report.ConcurrentChangeSkipped++
		return
	}
	report.DBUpdated++
}

func updateRegularUserCAS(ctx context.Context, db *gorm.DB, userID uint, currentAvatarURL, nextAvatarURL string) (bool, error) {
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	result := db.WithContext(ctx).Model(&models.User{}).
		Where("id = ? AND avatar_url = ?", userID, currentAvatarURL).
		Update("avatar_url", nextAvatarURL)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

type capturedDevDataAccount struct {
	ID                uint
	RegistryKey       string
	LocalUserID       uint
	AvatarObjectKey   string
	AvatarContentHash string
	UserAvatarURL     string
}

func runDevDataAccounts(ctx context.Context, db *gorm.DB, store ObjectStore, options Options, report *Report) error {
	var lastID uint
	for {
		var accounts []models.DevDataMirrorAccount
		query := db.WithContext(ctx).
			Where("enabled = ? AND id > ?", true, lastID).
			Order("id ASC").
			Limit(options.BatchSize).
			Find(&accounts)
		if query.Error != nil {
			return fmt.Errorf("scan DevData mirror accounts: %w", query.Error)
		}
		if len(accounts) == 0 {
			return nil
		}
		for _, account := range accounts {
			lastID = account.ID
			report.DevDataScanned++
			var user models.User
			if err := db.WithContext(ctx).Select("id", "avatar_url").First(&user, account.LocalUserID).Error; err != nil {
				report.Failed++
				continue
			}
			processDevDataAccount(ctx, db, store, options, capturedDevDataAccount{
				ID:                account.ID,
				RegistryKey:       account.RegistryKey,
				LocalUserID:       account.LocalUserID,
				AvatarObjectKey:   account.AvatarObjectKey,
				AvatarContentHash: account.AvatarContentHash,
				UserAvatarURL:     user.AvatarURL,
			}, report)
		}
		if len(accounts) < options.BatchSize {
			return nil
		}
	}
}

func processDevDataAccount(ctx context.Context, db *gorm.DB, store ObjectStore, options Options, captured capturedDevDataAccount, report *Report) {
	if captured.LocalUserID == 0 || strings.TrimSpace(captured.AvatarObjectKey) == "" {
		report.Empty++
		return
	}
	if devDataAvatarAlreadyV1(captured) {
		report.AlreadyV1++
		return
	}
	if strings.HasPrefix(captured.AvatarObjectKey, profileavatar.DevDataV1ObjectPrefix) {
		report.Failed++
		return
	}
	extension := avatarExtension(captured.AvatarObjectKey)
	expectedKey, err := devdata.BuildAvatarObjectKey(captured.RegistryKey, captured.AvatarContentHash, extension)
	if err != nil || expectedKey != captured.AvatarObjectKey {
		report.Failed++
		return
	}
	report.Eligible++

	body, _, err := store.Get(ctx, captured.AvatarObjectKey)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			report.MissingObject++
		}
		report.Failed++
		return
	}
	derivative, err := avatarimage.Optimize(body)
	if err != nil {
		report.Failed++
		return
	}
	report.Optimized++
	v1Key, err := devdata.BuildAvatarObjectKeyV1(captured.RegistryKey, derivative.ContentHash, derivative.Extension)
	if err != nil {
		report.Failed++
		return
	}
	needsUpload, ok := inspectTarget(ctx, store, v1Key, derivative, report)
	if !ok {
		return
	}
	if !options.Apply {
		return
	}
	if needsUpload {
		if err := store.Put(ctx, v1Key, derivative.Body, derivative.ContentType); err != nil {
			report.Failed++
			return
		}
		report.Uploaded++
	}

	if err := updateDevDataAtomically(ctx, db, captured, v1Key, derivative.ContentHash); err != nil {
		if errors.Is(err, ErrConcurrentChange) {
			report.ConcurrentChangeSkipped++
			return
		}
		report.Failed++
		return
	}
	report.DBUpdated++
}

var ErrConcurrentChange = errors.New("avatar metadata changed during backfill")

func updateDevDataAtomically(ctx context.Context, db *gorm.DB, captured capturedDevDataAccount, objectKey, contentHash string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account models.DevDataMirrorAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, captured.ID).Error; err != nil {
			return err
		}
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, captured.LocalUserID).Error; err != nil {
			return err
		}
		if !account.Enabled || account.RegistryKey != captured.RegistryKey || account.LocalUserID != captured.LocalUserID || account.AvatarObjectKey != captured.AvatarObjectKey || account.AvatarContentHash != captured.AvatarContentHash || user.AvatarURL != captured.UserAvatarURL {
			return ErrConcurrentChange
		}
		if result := tx.Model(&models.User{}).Where("id = ?", captured.LocalUserID).Update("avatar_url", profileavatar.FilesURLPrefix+objectKey); result.Error != nil {
			return result.Error
		}
		if result := tx.Model(&models.DevDataMirrorAccount{}).Where("id = ?", captured.ID).Updates(map[string]any{
			"avatar_object_key":   objectKey,
			"avatar_content_hash": contentHash,
		}); result.Error != nil {
			return result.Error
		}
		return nil
	})
}

func inspectTarget(ctx context.Context, store ObjectStore, objectKey string, derivative avatarimage.Derivative, report *Report) (bool, bool) {
	info, exists, err := store.Stat(ctx, objectKey)
	if err != nil {
		report.Failed++
		return false, false
	}
	if exists && info.Size == int64(len(derivative.Body)) && info.ContentType == derivative.ContentType {
		report.Reused++
		return false, true
	}
	report.WouldUpload++
	return true, true
}

func devDataAvatarAlreadyV1(captured capturedDevDataAccount) bool {
	if !strings.HasPrefix(captured.AvatarObjectKey, profileavatar.DevDataV1ObjectPrefix) {
		return false
	}
	extension := avatarExtension(captured.AvatarObjectKey)
	expected, err := devdata.BuildAvatarObjectKeyV1(captured.RegistryKey, captured.AvatarContentHash, extension)
	return err == nil && expected == captured.AvatarObjectKey && captured.UserAvatarURL == profileavatar.FilesURLPrefix+captured.AvatarObjectKey
}

func avatarExtension(objectKey string) string {
	for _, extension := range []string{".jpg", ".png", ".webp"} {
		if strings.HasSuffix(strings.ToLower(objectKey), extension) {
			return extension
		}
	}
	return ""
}

type minioObjectStore struct {
	client *minio.Client
	bucket string
}

func NewMinioObjectStore(client *minio.Client) (ObjectStore, error) {
	if client == nil {
		return nil, errors.New("MinIO client is not initialized")
	}
	return &minioObjectStore{client: client, bucket: config.StorageBucket()}, nil
}

// NewConfiguredMinioObjectStore builds a client without checking or creating
// the bucket, so the default dry-run remains read-only.
func NewConfiguredMinioObjectStore() (ObjectStore, error) {
	client, err := minio.New(config.StorageEndpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(config.StorageAccessKey(), config.StorageSecretKey(), ""),
		Secure: config.StorageUseSSL(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize MinIO client: %w", err)
	}
	return NewMinioObjectStore(client)
}

func (s *minioObjectStore) Get(ctx context.Context, objectKey string) ([]byte, AvatarObjectInfo, error) {
	if s == nil || s.client == nil {
		return nil, AvatarObjectInfo{}, errors.New("avatar object store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	object, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, AvatarObjectInfo{}, fmt.Errorf("get avatar object: %w", err)
	}
	defer object.Close()
	info, err := object.Stat()
	if err != nil {
		if isMissingObject(err) {
			return nil, AvatarObjectInfo{}, ErrObjectNotFound
		}
		return nil, AvatarObjectInfo{}, fmt.Errorf("stat avatar object: %w", err)
	}
	if info.Size <= 0 {
		return nil, AvatarObjectInfo{}, errors.New("avatar object is empty")
	}
	if info.Size > avatarimage.MaxSourceBytes {
		return nil, AvatarObjectInfo{}, errors.New("avatar object exceeds source size limit")
	}
	body, err := io.ReadAll(io.LimitReader(object, int64(avatarimage.MaxSourceBytes)+1))
	if err != nil {
		return nil, AvatarObjectInfo{}, fmt.Errorf("read avatar object: %w", err)
	}
	return body, AvatarObjectInfo{Size: info.Size, ContentType: info.ContentType}, nil
}

func (s *minioObjectStore) Stat(ctx context.Context, objectKey string) (AvatarObjectInfo, bool, error) {
	if s == nil || s.client == nil {
		return AvatarObjectInfo{}, false, errors.New("avatar object store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if isMissingObject(err) {
			return AvatarObjectInfo{}, false, nil
		}
		return AvatarObjectInfo{}, false, fmt.Errorf("stat avatar object: %w", err)
	}
	return AvatarObjectInfo{Size: info.Size, ContentType: info.ContentType}, true, nil
}

func (s *minioObjectStore) Put(ctx context.Context, objectKey string, body []byte, contentType string) error {
	if s == nil || s.client == nil {
		return errors.New("avatar object store is not initialized")
	}
	if len(body) == 0 || len(body) > avatarimage.MaxSourceBytes {
		return errors.New("avatar object body has an invalid size")
	}
	extension := avatarExtension(objectKey)
	if (extension == ".jpg" && contentType != "image/jpeg") || (extension == ".png" && contentType != "image/png") {
		return errors.New("avatar object content type is invalid")
	}
	if extension != ".jpg" && extension != ".png" {
		return errors.New("avatar object extension is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put avatar object: %w", err)
	}
	return nil
}

func isMissingObject(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.StatusCode == http.StatusNotFound || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}
