package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
)

var ErrLaunchState = errors.New("managed launch state could not be persisted")

func (store *Store) PrepareLaunch(ctx context.Context, request launch.PrepareRequest) (launch.Plan, error) {
	if store == nil || store.db == nil {
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, ErrLaunchState)
	}
	if err := profile.ValidateAlias(request.Alias); err != nil {
		return launch.Plan{}, err
	}
	if strings.TrimSpace(request.Executable) == "" || !filepath.IsAbs(request.Executable) || strings.TrimSpace(request.WorkingDirectory) == "" || !filepath.IsAbs(request.WorkingDirectory) {
		return launch.Plan{}, apperrors.New(apperrors.LaunchPlanInvalid, launch.ErrPlanInvalid)
	}
	ctx = contextOrBackground(ctx)

	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var profileID, status string
	var homeID, ownership sql.NullString
	var ciphertext []byte
	err = tx.QueryRowContext(ctx, `SELECT ip.profile_id, ip.status, ip.identity_home_id,
		h.ownership, h.location_ciphertext
		FROM identity_profiles ip
		JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		WHERE a.alias = ? COLLATE NOCASE AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = ip.profile_id)`, request.Alias).Scan(&profileID, &status, &homeID, &ownership, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return launch.Plan{}, apperrors.New(apperrors.LaunchProfileNotFound, launch.ErrProfileNotFound)
	}
	if err != nil {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	if status != string(profile.StatusReady) {
		rollback()
		return launch.Plan{}, apperrors.New(apperrors.LaunchProfileUnavailable, launch.ErrProfileUnavailable)
	}
	if !homeID.Valid || strings.TrimSpace(homeID.String) == "" || !ownership.Valid || (ownership.String != string(profile.HomeOwnershipManaged) && ownership.String != string(profile.HomeOwnershipReferenced)) || len(ciphertext) == 0 {
		rollback()
		return launch.Plan{}, apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	}
	secureVault, err := store.requireVault()
	if err != nil {
		rollback()
		return launch.Plan{}, err
	}
	identityHome, err := decryptField(ctx, secureVault, ciphertext, identityHomeAAD(homeID.String))
	if err != nil {
		rollback()
		return launch.Plan{}, err
	}
	identityHome = filepath.Clean(identityHome)
	if !filepath.IsAbs(identityHome) {
		rollback()
		return launch.Plan{}, apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	}

	now := store.clock.Now().UTC()
	if now.IsZero() {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, errors.New("launch clock returned zero")))
	}
	managedLaunchID, err := newLaunchIdentifier("launch")
	if err != nil {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	leaseID, err := newLaunchIdentifier("lease")
	if err != nil {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	encodedNow := formatStoredTime(now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_launches (
		managed_launch_id, profile_id, lease_id, project_identity_id, state,
		started_at, ended_at, process_id, exit_status
	) VALUES (?, ?, ?, NULL, 'pending', ?, NULL, NULL, NULL)`, managedLaunchID, profileID, leaseID, encodedNow); err != nil {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return launch.Plan{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	return launch.Plan{
		LeaseID:          leaseID,
		Executable:       filepath.Clean(request.Executable),
		WorkingDirectory: filepath.Clean(request.WorkingDirectory),
		Arguments:        append([]string(nil), request.Arguments...),
		Environment:      map[string]string{"CODEX_HOME": identityHome},
	}, nil
}

func (store *Store) MarkManagedLaunchStarted(ctx context.Context, leaseID string, processID int) error {
	if store == nil || store.db == nil || strings.TrimSpace(leaseID) == "" || processID <= 0 {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE managed_launches SET state = 'running', process_id = ? WHERE lease_id = ? AND state = 'pending'`, processID, leaseID)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	if affected, err := result.RowsAffected(); err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	} else if affected == 0 {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	return nil
}

func (store *Store) MarkManagedLaunchExited(ctx context.Context, leaseID string, exitStatus int) error {
	if store == nil || store.db == nil || strings.TrimSpace(leaseID) == "" {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	if !launch.ValidProcessStatus(exitStatus) {
		return apperrors.New(apperrors.LaunchProcessStatusInvalid, launch.ErrProcessStatusInvalid)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, errors.New("launch clock returned zero")))
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE managed_launches SET state = 'exited', exit_status = ?, ended_at = ? WHERE lease_id = ? AND state = 'running'`, exitStatus, formatStoredTime(now), leaseID)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	if affected, err := result.RowsAffected(); err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	} else if affected == 0 {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	return nil
}

func (store *Store) MarkManagedLaunchAbandoned(ctx context.Context, leaseID string) error {
	if store == nil || store.db == nil || strings.TrimSpace(leaseID) == "" {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, errors.New("launch clock returned zero")))
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE managed_launches SET state = 'abandoned', ended_at = ? WHERE lease_id = ? AND state IN ('pending', 'running')`, formatStoredTime(now), leaseID)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	if affected, err := result.RowsAffected(); err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	} else if affected == 0 {
		return apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	return nil
}

func (store *Store) ReconcileManagedLaunches(ctx context.Context, inspector launch.ProcessInspector) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreReadFailed, ErrLaunchState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT lease_id, state, process_id FROM managed_launches WHERE state IN ('pending', 'running')`)
	if err != nil {
		return coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	var abandoned []string
	for rows.Next() {
		var leaseID, state string
		var processID sql.NullInt64
		if err := rows.Scan(&leaseID, &state, &processID); err != nil {
			_ = rows.Close()
			return coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
		}
		if state == string(launch.StatePending) || !processID.Valid || processID.Int64 <= 0 || inspector == nil {
			continue
		}
		running, err := inspector.IsRunning(int(processID.Int64))
		if err != nil {
			_ = rows.Close()
			return coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
		}
		if !running {
			abandoned = append(abandoned, leaseID)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	if err := rows.Close(); err != nil {
		return coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	if len(abandoned) == 0 {
		return nil
	}
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, errors.New("launch clock returned zero")))
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	for _, leaseID := range abandoned {
		if _, err := tx.ExecContext(ctx, `UPDATE managed_launches SET state = 'abandoned', ended_at = ? WHERE lease_id = ? AND state IN ('pending', 'running')`, formatStoredTime(now), leaseID); err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrLaunchState, err))
	}
	return nil
}

func (store *Store) GetManagedLaunch(ctx context.Context, leaseID string) (launch.ManagedLaunch, error) {
	if store == nil || store.db == nil || strings.TrimSpace(leaseID) == "" {
		return launch.ManagedLaunch{}, apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var record launch.ManagedLaunch
	var state string
	var processID, exitStatus sql.NullInt64
	var startedAt string
	var endedAt sql.NullString
	err := store.db.QueryRowContext(ctx, `SELECT managed_launch_id, profile_id, lease_id, state,
		process_id, exit_status, started_at, ended_at FROM managed_launches WHERE lease_id = ?`, leaseID).Scan(
		&record.ID, &record.ProfileID, &record.LeaseID, &state, &processID, &exitStatus, &startedAt, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return launch.ManagedLaunch{}, apperrors.New(apperrors.LaunchLeaseInvalid, launch.ErrLeaseInvalid)
	}
	if err != nil {
		return launch.ManagedLaunch{}, coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	record.State = launch.State(state)
	if processID.Valid {
		record.ProcessID = int(processID.Int64)
	}
	if exitStatus.Valid {
		value := int(exitStatus.Int64)
		record.ExitStatus = &value
	}
	record.StartedAt, err = parseStoredTime(startedAt)
	if err != nil {
		return launch.ManagedLaunch{}, coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, err))
	}
	if endedAt.Valid {
		value, parseErr := parseStoredTime(endedAt.String)
		if parseErr != nil {
			return launch.ManagedLaunch{}, coded(apperrors.StoreReadFailed, errors.Join(ErrLaunchState, parseErr))
		}
		record.EndedAt = &value
	}
	return record, nil
}

func newLaunchIdentifier(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(random[:]), nil
}

var _ launch.Repository = (*Store)(nil)
