package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeBacklogMigration = `
CREATE TABLE initiatives (
    handle TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    managed_run_group_id TEXT NOT NULL,
    title_ref TEXT NOT NULL,
    state TEXT NOT NULL,
    base_revision_set_json TEXT NOT NULL,
    components_json TEXT NOT NULL,
    edges_json TEXT NOT NULL,
    contract_artifacts_json TEXT NOT NULL,
    integration_policy_id TEXT NOT NULL,
    integration_owner_task TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX initiatives_state_handle_idx ON initiatives(state, handle);
CREATE UNIQUE INDEX initiatives_bound_group_idx ON initiatives(managed_run_group_id)
WHERE managed_run_group_id <> '';
CREATE TABLE backlog_items (
    handle TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    repository_id TEXT NOT NULL,
    shape TEXT NOT NULL,
    requested_outcome TEXT NOT NULL,
    depends_on_json TEXT NOT NULL,
    priority TEXT NOT NULL,
    readiness TEXT NOT NULL,
    source_conversation_ref TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX backlog_items_readiness_handle_idx ON backlog_items(readiness, handle);
INSERT INTO schema_migrations(version, applied_at)
VALUES (34, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var _ application.InitiativeRepository = (*Store)(nil)
var _ application.BacklogRepository = (*Store)(nil)

// CreateInitiative atomically inserts one validated initiative record.
func (store *Store) CreateInitiative(ctx context.Context, initiative domain.DevelopmentInitiative) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create initiative: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := insertInitiative(ctx, transaction, initiative); err != nil {
		return fmt.Errorf("create initiative: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit create initiative: %w", err)
	}
	return nil
}

func insertInitiative(ctx context.Context, target execer, initiative domain.DevelopmentInitiative) error {
	if err := initiative.Validate(); err != nil {
		return err
	}
	baseRevisions, err := json.Marshal(initiative.BaseRevisionSet)
	if err != nil {
		return fmt.Errorf("encode initiative base revisions: %w", err)
	}
	components, err := json.Marshal(initiative.Components)
	if err != nil {
		return fmt.Errorf("encode initiative components: %w", err)
	}
	edges, err := json.Marshal(initiative.Edges)
	if err != nil {
		return fmt.Errorf("encode initiative edges: %w", err)
	}
	contractArtifacts, err := json.Marshal(initiative.ContractArtifacts)
	if err != nil {
		return fmt.Errorf("encode initiative contract artifacts: %w", err)
	}
	const statement = `INSERT INTO initiatives (
        handle, schema_version, managed_run_group_id, title_ref, state,
        base_revision_set_json, components_json, edges_json, contract_artifacts_json,
        integration_policy_id, integration_owner_task, state_version, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = target.ExecContext(ctx, statement,
		initiative.Handle, initiative.SchemaVersion, initiative.ManagedRunGroupID, initiative.TitleRef,
		initiative.State, string(baseRevisions), string(components), string(edges), string(contractArtifacts),
		initiative.IntegrationPolicyID, initiative.IntegrationOwnerTask, initiative.StateVersion,
		formatTime(initiative.CreatedAt), formatTime(initiative.UpdatedAt),
	)
	if isConstraintError(err) {
		return application.ErrConflict
	}
	return err
}

// GetInitiative returns one validated initiative by its opaque handle.
func (store *Store) GetInitiative(ctx context.Context, handle string) (domain.DevelopmentInitiative, error) {
	return getInitiative(ctx, store.db, handle)
}

func getInitiative(
	ctx context.Context,
	source queryer,
	handle string,
) (domain.DevelopmentInitiative, error) {
	const query = `SELECT handle, schema_version, managed_run_group_id, title_ref, state,
        base_revision_set_json, components_json, edges_json, contract_artifacts_json,
        integration_policy_id, integration_owner_task, state_version, created_at, updated_at
        FROM initiatives WHERE handle = ?`
	initiative, err := scanInitiative(source.QueryRowContext(ctx, query, handle))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DevelopmentInitiative{}, fmt.Errorf("get initiative: %w", application.ErrNotFound)
	}
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("get initiative: %w", err)
	}
	return initiative, nil
}

// ListInitiatives returns validated initiative records ordered by handle.
func (store *Store) ListInitiatives(ctx context.Context) ([]domain.DevelopmentInitiative, error) {
	return listInitiatives(ctx, store.db)
}

func listInitiatives(
	ctx context.Context,
	source queryer,
) (initiatives []domain.DevelopmentInitiative, resultErr error) {
	const query = `SELECT handle, schema_version, managed_run_group_id, title_ref, state,
        base_revision_set_json, components_json, edges_json, contract_artifacts_json,
        integration_policy_id, integration_owner_task, state_version, created_at, updated_at
        FROM initiatives ORDER BY handle`
	rows, err := source.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list initiatives: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	initiatives = make([]domain.DevelopmentInitiative, 0)
	for rows.Next() {
		initiative, err := scanInitiative(rows)
		if err != nil {
			return nil, fmt.Errorf("list initiatives: %w", err)
		}
		initiatives = append(initiatives, initiative)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list initiatives: %w", err)
	}
	return initiatives, nil
}

func listInitiativePage(
	ctx context.Context,
	source queryer,
	filter application.InitiativeFilter,
) (initiatives []domain.DevelopmentInitiative, nextCursor string, resultErr error) {
	const selectPage = `SELECT handle, schema_version, managed_run_group_id, title_ref, state,
        base_revision_set_json, components_json, edges_json, contract_artifacts_json,
        integration_policy_id, integration_owner_task, state_version, created_at, updated_at
        FROM initiatives`
	const pageOrder = ` ORDER BY handle LIMIT ?`
	var rows *sql.Rows
	var err error
	if filter.State != "" {
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ? AND state = ?`+pageOrder,
			filter.AfterHandle, filter.State, filter.Limit+1,
		)
	} else {
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ?`+pageOrder,
			filter.AfterHandle, filter.Limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list initiative page: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	initiatives = make([]domain.DevelopmentInitiative, 0, filter.Limit+1)
	for rows.Next() {
		initiative, err := scanInitiative(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list initiative page: %w", err)
		}
		initiatives = append(initiatives, initiative)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list initiative page: %w", err)
	}
	if len(initiatives) <= filter.Limit {
		return initiatives, "", nil
	}
	nextCursor = initiatives[filter.Limit-1].Handle
	return initiatives[:filter.Limit], nextCursor, nil
}

func scanInitiative(row rowScanner) (domain.DevelopmentInitiative, error) {
	var initiative domain.DevelopmentInitiative
	var baseRevisions, components, edges, contractArtifacts string
	var createdAt, updatedAt string
	if err := row.Scan(
		&initiative.Handle, &initiative.SchemaVersion, &initiative.ManagedRunGroupID, &initiative.TitleRef,
		&initiative.State, &baseRevisions, &components, &edges, &contractArtifacts,
		&initiative.IntegrationPolicyID, &initiative.IntegrationOwnerTask, &initiative.StateVersion,
		&createdAt, &updatedAt,
	); err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if err := json.Unmarshal([]byte(baseRevisions), &initiative.BaseRevisionSet); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("decode initiative base revisions: %w", err)
	}
	if err := json.Unmarshal([]byte(components), &initiative.Components); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("decode initiative components: %w", err)
	}
	if err := json.Unmarshal([]byte(edges), &initiative.Edges); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("decode initiative edges: %w", err)
	}
	if err := json.Unmarshal([]byte(contractArtifacts), &initiative.ContractArtifacts); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("decode initiative contract artifacts: %w", err)
	}
	var err error
	initiative.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("parse initiative created time: %w", err)
	}
	initiative.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("parse initiative updated time: %w", err)
	}
	if err := initiative.Validate(); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("validate stored initiative: %w", err)
	}
	return initiative, nil
}

// CreateBacklogItem atomically inserts one validated request without run authority.
func (store *Store) CreateBacklogItem(ctx context.Context, item domain.BacklogItem) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create backlog item: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := insertBacklogItem(ctx, transaction, item); err != nil {
		return fmt.Errorf("create backlog item: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit create backlog item: %w", err)
	}
	return nil
}

func insertBacklogItem(ctx context.Context, target execer, item domain.BacklogItem) error {
	if err := item.Validate(); err != nil {
		return err
	}
	dependencies, err := json.Marshal(item.DependsOn)
	if err != nil {
		return fmt.Errorf("encode backlog dependencies: %w", err)
	}
	const statement = `INSERT INTO backlog_items (
        handle, schema_version, repository_id, shape, requested_outcome, depends_on_json,
        priority, readiness, source_conversation_ref, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = target.ExecContext(ctx, statement,
		item.Handle, item.SchemaVersion, item.RepositoryID, item.Shape, item.RequestedOutcome,
		string(dependencies), item.Priority, item.Readiness, item.SourceConversationRef,
		formatTime(item.CreatedAt), formatTime(item.UpdatedAt),
	)
	if isConstraintError(err) {
		return application.ErrConflict
	}
	return err
}

// GetBacklogItem returns one validated backlog request by its opaque handle.
func (store *Store) GetBacklogItem(ctx context.Context, handle string) (domain.BacklogItem, error) {
	return getBacklogItem(ctx, store.db, handle)
}

func getBacklogItem(ctx context.Context, source queryer, handle string) (domain.BacklogItem, error) {
	const query = `SELECT handle, schema_version, repository_id, shape, requested_outcome,
        depends_on_json, priority, readiness, source_conversation_ref, created_at, updated_at
        FROM backlog_items WHERE handle = ?`
	item, err := scanBacklogItem(source.QueryRowContext(ctx, query, handle))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, fmt.Errorf("get backlog item: %w", application.ErrNotFound)
	}
	if err != nil {
		return domain.BacklogItem{}, fmt.Errorf("get backlog item: %w", err)
	}
	return item, nil
}

// ListBacklogItems returns validated backlog requests ordered by handle.
func (store *Store) ListBacklogItems(ctx context.Context) (items []domain.BacklogItem, resultErr error) {
	return listBacklogItems(ctx, store.db)
}

func listBacklogItems(
	ctx context.Context,
	source queryer,
) (items []domain.BacklogItem, resultErr error) {
	const query = `SELECT handle, schema_version, repository_id, shape, requested_outcome,
        depends_on_json, priority, readiness, source_conversation_ref, created_at, updated_at
        FROM backlog_items ORDER BY handle`
	rows, err := source.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list backlog items: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	items = make([]domain.BacklogItem, 0)
	for rows.Next() {
		item, err := scanBacklogItem(rows)
		if err != nil {
			return nil, fmt.Errorf("list backlog items: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list backlog items: %w", err)
	}
	return items, nil
}

func listBacklogPage(
	ctx context.Context,
	source queryer,
	filter application.BacklogFilter,
) (items []domain.BacklogItem, nextCursor string, resultErr error) {
	const selectPage = `SELECT handle, schema_version, repository_id, shape, requested_outcome,
        depends_on_json, priority, readiness, source_conversation_ref, created_at, updated_at
        FROM backlog_items`
	const pageOrder = ` ORDER BY handle LIMIT ?`
	var rows *sql.Rows
	var err error
	switch {
	case filter.RepositoryID != "" && filter.Readiness != "":
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ? AND repository_id = ? AND readiness = ?`+pageOrder,
			filter.AfterHandle, filter.RepositoryID, filter.Readiness, filter.Limit,
		)
	case filter.RepositoryID != "":
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ? AND repository_id = ?`+pageOrder,
			filter.AfterHandle, filter.RepositoryID, filter.Limit,
		)
	case filter.Readiness != "":
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ? AND readiness = ?`+pageOrder,
			filter.AfterHandle, filter.Readiness, filter.Limit,
		)
	default:
		rows, err = source.QueryContext(ctx,
			selectPage+` WHERE handle > ?`+pageOrder,
			filter.AfterHandle, filter.Limit,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list backlog page: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	items = make([]domain.BacklogItem, 0, filter.Limit)
	nextCursor = filter.AfterHandle
	for rows.Next() {
		item, err := scanBacklogItem(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list backlog page: %w", err)
		}
		items = append(items, item)
		nextCursor = item.Handle
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list backlog page: %w", err)
	}
	return items, nextCursor, nil
}

func scanBacklogItem(row rowScanner) (domain.BacklogItem, error) {
	var item domain.BacklogItem
	var dependencies, createdAt, updatedAt string
	if err := row.Scan(
		&item.Handle, &item.SchemaVersion, &item.RepositoryID, &item.Shape, &item.RequestedOutcome,
		&dependencies, &item.Priority, &item.Readiness, &item.SourceConversationRef, &createdAt, &updatedAt,
	); err != nil {
		return domain.BacklogItem{}, err
	}
	if err := json.Unmarshal([]byte(dependencies), &item.DependsOn); err != nil {
		return domain.BacklogItem{}, fmt.Errorf("decode backlog dependencies: %w", err)
	}
	var err error
	item.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.BacklogItem{}, fmt.Errorf("parse backlog created time: %w", err)
	}
	item.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.BacklogItem{}, fmt.Errorf("parse backlog updated time: %w", err)
	}
	if err := item.Validate(); err != nil {
		return domain.BacklogItem{}, fmt.Errorf("validate stored backlog item: %w", err)
	}
	return item, nil
}
