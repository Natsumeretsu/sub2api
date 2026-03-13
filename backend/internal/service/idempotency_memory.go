package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

type memoryIdempotencyRepository struct {
	mu     sync.Mutex
	nextID int64
	data   map[string]*IdempotencyRecord
}

func NewMemoryIdempotencyRepository() IdempotencyRepository {
	return &memoryIdempotencyRepository{
		nextID: 1,
		data:   make(map[string]*IdempotencyRecord),
	}
}

func (r *memoryIdempotencyRepository) key(scope, hash string) string {
	return scope + "|" + hash
}

func cloneIdempotencyRecord(in *IdempotencyRecord) *IdempotencyRecord {
	if in == nil {
		return nil
	}
	out := *in
	if in.ResponseStatus != nil {
		v := *in.ResponseStatus
		out.ResponseStatus = &v
	}
	if in.ResponseBody != nil {
		v := *in.ResponseBody
		out.ResponseBody = &v
	}
	if in.ErrorReason != nil {
		v := *in.ErrorReason
		out.ErrorReason = &v
	}
	if in.LockedUntil != nil {
		v := *in.LockedUntil
		out.LockedUntil = &v
	}
	return &out
}

func (r *memoryIdempotencyRepository) maybeCleanupLocked(now time.Time) {
	for key, record := range r.data {
		if record == nil || !record.ExpiresAt.After(now) {
			delete(r.data, key)
		}
	}
}

func (r *memoryIdempotencyRepository) CreateProcessing(_ context.Context, record *IdempotencyRecord) (bool, error) {
	if r == nil {
		return false, errors.New("memory idempotency repo is nil")
	}
	if record == nil {
		return false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.maybeCleanupLocked(now)
	key := r.key(record.Scope, record.IdempotencyKeyHash)
	if _, ok := r.data[key]; ok {
		return false, nil
	}
	stored := cloneIdempotencyRecord(record)
	stored.ID = r.nextID
	stored.CreatedAt = now
	stored.UpdatedAt = now
	r.nextID++
	r.data[key] = stored

	record.ID = stored.ID
	record.CreatedAt = stored.CreatedAt
	record.UpdatedAt = stored.UpdatedAt
	return true, nil
}

func (r *memoryIdempotencyRepository) GetByScopeAndKeyHash(_ context.Context, scope, keyHash string) (*IdempotencyRecord, error) {
	if r == nil {
		return nil, errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.maybeCleanupLocked(now)
	return cloneIdempotencyRecord(r.data[r.key(scope, keyHash)]), nil
}

func (r *memoryIdempotencyRepository) TryReclaim(_ context.Context, id int64, fromStatus string, now, newLockedUntil, newExpiresAt time.Time) (bool, error) {
	if r == nil {
		return false, errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.maybeCleanupLocked(now)
	for _, record := range r.data {
		if record == nil || record.ID != id {
			continue
		}
		if record.Status != fromStatus {
			return false, nil
		}
		if record.LockedUntil != nil && record.LockedUntil.After(now) {
			return false, nil
		}
		record.Status = IdempotencyStatusProcessing
		record.LockedUntil = &newLockedUntil
		record.ErrorReason = nil
		record.ExpiresAt = newExpiresAt
		record.UpdatedAt = now
		return true, nil
	}
	return false, nil
}

func (r *memoryIdempotencyRepository) ExtendProcessingLock(_ context.Context, id int64, requestFingerprint string, newLockedUntil, newExpiresAt time.Time) (bool, error) {
	if r == nil {
		return false, errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.maybeCleanupLocked(now)
	for _, record := range r.data {
		if record == nil || record.ID != id {
			continue
		}
		if record.Status != IdempotencyStatusProcessing || record.RequestFingerprint != requestFingerprint {
			return false, nil
		}
		record.LockedUntil = &newLockedUntil
		record.ExpiresAt = newExpiresAt
		record.UpdatedAt = now
		return true, nil
	}
	return false, nil
}

func (r *memoryIdempotencyRepository) MarkSucceeded(_ context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	if r == nil {
		return errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.maybeCleanupLocked(now)
	for _, record := range r.data {
		if record == nil || record.ID != id {
			continue
		}
		record.Status = IdempotencyStatusSucceeded
		record.ResponseStatus = &responseStatus
		record.ResponseBody = &responseBody
		record.ErrorReason = nil
		record.LockedUntil = nil
		record.ExpiresAt = expiresAt
		record.UpdatedAt = now
		return nil
	}
	return errors.New("record not found")
}

func (r *memoryIdempotencyRepository) MarkFailedRetryable(_ context.Context, id int64, errorReason string, lockedUntil, expiresAt time.Time) error {
	if r == nil {
		return errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.maybeCleanupLocked(now)
	for _, record := range r.data {
		if record == nil || record.ID != id {
			continue
		}
		record.Status = IdempotencyStatusFailedRetryable
		record.ErrorReason = &errorReason
		record.LockedUntil = &lockedUntil
		record.ExpiresAt = expiresAt
		record.UpdatedAt = now
		return nil
	}
	return errors.New("record not found")
}

func (r *memoryIdempotencyRepository) DeleteExpired(_ context.Context, now time.Time, _ int) (int64, error) {
	if r == nil {
		return 0, errors.New("memory idempotency repo is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var deleted int64
	for key, record := range r.data {
		if record != nil && !record.ExpiresAt.After(now) {
			delete(r.data, key)
			deleted++
		}
	}
	return deleted, nil
}
