package controllers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

type postSearchCursorV1 struct {
	Version      int       `json:"v"`
	CriteriaHash string    `json:"criteria_hash"`
	AnchorAt     time.Time `json:"anchor_at"`
	CreatedAt    time.Time `json:"created_at"`
	PostID       uint      `json:"id"`
}

type postSearchCursorCriteria struct {
	Query    string  `json:"q"`
	AuthorID *uint   `json:"author_id"`
	From     *string `json:"from"`
	To       *string `json:"to"`
	Sort     string  `json:"sort"`
}

func postSearchCriteriaHash(criteria postSearchCursorCriteria) (string, error) {
	encoded, err := json.Marshal(criteria)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func encodePostSearchCursor(cursor postSearchCursorV1) (string, error) {
	if err := validatePostSearchCursor(cursor); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePostSearchCursor(encoded string) (postSearchCursorV1, error) {
	if encoded == "" {
		return postSearchCursorV1{}, errors.New("cursor is empty")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return postSearchCursorV1{}, errors.New("cursor is malformed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor postSearchCursorV1
	if err := decoder.Decode(&cursor); err != nil {
		return postSearchCursorV1{}, errors.New("cursor is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return postSearchCursorV1{}, errors.New("cursor contains trailing JSON")
	}
	if err := validatePostSearchCursor(cursor); err != nil {
		return postSearchCursorV1{}, err
	}
	return cursor, nil
}

func validatePostSearchCursor(cursor postSearchCursorV1) error {
	if cursor.Version != 1 {
		return errors.New("cursor version is unsupported")
	}
	hash, err := hex.DecodeString(cursor.CriteriaHash)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("cursor criteria hash is invalid")
	}
	if cursor.AnchorAt.IsZero() || cursor.CreatedAt.IsZero() {
		return errors.New("cursor timestamps are invalid")
	}
	if cursor.PostID == 0 {
		return errors.New("cursor post ID is invalid")
	}
	return nil
}
