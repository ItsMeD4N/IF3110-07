export type PlatformPermission =
  | 'platform.users.manage'
  | 'platform.groups.manage'
  | 'platform.grants.manage'
  | 'platform.connections.manage';

export type DatabasePermission =
  | 'db.schema.view'
  | 'db.data.view'
  | 'db.migrations.view'
  | 'db.migrations.create'
  | 'db.migrations.edit'
  | 'db.migrations.execute';

export type AccessLevel = 'none' | 'view_only' | 'view_edit';

export interface LedGroupSummary {
  id: number;
  name: string;
}

export interface AuthUser {
  id: string;
  username: string;
  is_platform_admin: boolean;
  is_active?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface AuthMeResponse {
  id: string;
  username: string;
  is_platform_admin: boolean;
  permissions: PlatformPermission[];
  led_groups?: LedGroupSummary[];
}

export interface Group {
  id: number;
  name: string;
  description: string;
  leader_user_id?: string | null;
  leader_username?: string | null;
  member_count: number;
  grant_count: number;
  created_at: string;
  updated_at: string;
}

export interface GroupMember {
  user_id: string;
  username: string;
  is_platform_admin: boolean;
  is_active: boolean;
  added_at: string;
}

export interface GroupGrant {
  group_id: number;
  group_name: string;
  connection_id: number;
  connection_name: string;
  access_level: AccessLevel;
  granted_at: string;
  updated_at: string;
}

export interface GroupDetail extends Group {
  members: GroupMember[];
  grants: GroupGrant[];
}

export type SSLMode = 'disable' | 'allow' | 'prefer' | 'require' | 'verify-ca' | 'verify-full';

export interface DBConnection {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  database_name: string;
  username: string;
  ssl_mode: SSLMode;
  created_at: string;
  updated_at: string;
}

export interface ConnectionInput {
  name: string;
  description: string;
  host: string;
  port: number;
  database_name: string;
  username: string;
  password?: string;
  ssl_mode: SSLMode;
  test_before_save?: boolean;
  connection_id?: number;
}

export interface ConnectionTestResult {
  ok: boolean;
  server_version?: string;
  latency_ms?: number;
  error?: string;
}

export interface MyDatabase {
  id: number;
  name: string;
  description: string;
  database_name: string;
  access_level: AccessLevel;
  access_label: string;
  permissions: DatabasePermission[];
  via_groups?: string[];
}

export interface SchemaColumn {
  name: string;
  type: string;
  nullable: boolean;
  default?: string;
  ordinal: number;
}

export interface SchemaConstraint {
  name: string;
  type: string;
  definition: string;
}

export interface SchemaIndex {
  name: string;
  definition: string;
}

export interface SchemaTable {
  name: string;
  type: string;
  columns: SchemaColumn[];
  constraints: SchemaConstraint[];
  indexes: SchemaIndex[];
}

export interface SchemaResponse {
  database_id: number;
  schemas: string[];
  selected_schema: string;
  tables: SchemaTable[];
}

export interface TableDataResponse {
  schema: string;
  table: string;
  columns: string[];
  rows: unknown[][];
  total: number;
  limit: number;
  offset: number;
}

export type MigrationAction =
  | 'up'
  | 'rollback'
  | 'forward'
  | 'batch_up'
  | 'batch_resume'
  | 'batch_revert';

export type ExecutionMode = 'normal' | 'batch';

export type WorkflowStatus =
  | 'draft'
  | 'pending_review'
  | 'approved'
  | 'rejected'
  | 'queued'
  | 'running'
  | 'applied'
  | 'rolled_back'
  | 'failed'
  | 'partial'
  | 'partially_reverted'
  | 'uncertain';

export type ExecutionStatus =
  | 'pending'
  | 'running'
  | 'success'
  | 'failed'
  | 'partial'
  | 'partially_reverted'
  | 'reverted'
  | 'uncertain';

export type JobStatus =
  | 'pending'
  | 'running'
  | 'retrying'
  | 'completed'
  | 'failed'
  | 'partial'
  | 'partially_reverted'
  | 'uncertain'
  | 'cancelled';

export interface BatchConfig {
  schema?: string;
  table: string;
  cursor_column: string;
  batch_size: number;
  where_clause?: string;
  capture_columns?: string[];
  batch_delay_ms?: number;
}

export interface MigrationDefinition {
  id: number;
  database_id: number;
  version: number;
  name: string;
  sql_up: string;
  sql_down?: string;
  is_irreversible: boolean;
  irreversible_reason?: string;
  execution_mode: ExecutionMode;
  batch_config?: BatchConfig;
  workflow_status: WorkflowStatus;
  is_locked_immutable: boolean;
  revision: number;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
  is_active: boolean;
  active_execution_id?: number;
  can_rollback: boolean;
  can_forward: boolean;
}

export interface MigrationDefinitionInput {
  name: string;
  sql_up: string;
  sql_down: string;
  is_irreversible: boolean;
  irreversible_reason: string;
  execution_mode: ExecutionMode;
  batch_config?: BatchConfig;
}

export interface BatchCheckpoint {
  id: number;
  execution_id: number;
  root_execution_id: number;
  database_id: number;
  migration_id: number;
  batch_number: number;
  cursor_start: string;
  cursor_end: string;
  rows_affected: number;
  status: 'committed' | 'failed' | 'reverted' | 'revert_failed';
  attempt: number;
  error?: string;
  sqlstate?: string;
  duration_ms: number;
  committed_at?: string;
  reverted_at?: string;
  updated_at: string;
}

export interface MigrationExecution {
  execution_id: number;
  database_id: number;
  migration_id: number;
  migration_version: number;
  migration_revision: number;
  migration_name: string;
  action: MigrationAction;
  execution_mode: ExecutionMode;
  status: ExecutionStatus;
  sql_executed: string;
  actor: string;
  reverted_execution_id?: number;
  parent_execution_id?: number;
  job_id?: string;
  attempt: number;
  total_batches: number;
  completed_batches: number;
  rows_affected: number;
  reverted_batches: number;
  reverted_rows: number;
  last_cursor?: string;
  scope_max_cursor?: string;
  error?: string;
  sqlstate?: string;
  correlation_id?: string;
  trace_id?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  checkpoints?: BatchCheckpoint[];
}

export interface MigrationJob {
  id: string;
  database_id: number;
  migration_id: number;
  execution_id?: number;
  action: MigrationAction;
  actor: string;
  idempotency_key: string;
  status: JobStatus;
  attempt: number;
  max_attempts: number;
  run_at: string;
  locked_by?: string;
  lease_expires_at?: string;
  last_error?: string;
  last_sqlstate?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  updated_at: string;
}

export interface EnqueueResponse {
  message: string;
  job_id: string;
  deduplicated: boolean;
  job: MigrationJob;
}

export interface LockInfo {
  database_id: number;
  locked: boolean;
  fencing_version?: number;
  owner_id?: string;
  job_id?: string;
  execution_id?: number;
  state?: 'held' | 'blocked_partial' | 'blocked_uncertain';
  reason?: string;
  acquired_at?: string;
  heartbeat_at?: string;
  expires_at?: string;
  expired: boolean;
}

export interface PlanSummary {
  scan_types: string[] | null;
  indexes_used: string[] | null;
  join_strategies: string[] | null;
  startup_cost: number;
  total_cost: number;
  actual_time_ms?: number;
  planning_time_ms?: number;
  rows_planned: number;
  rows_processed?: number;
  has_seq_scan: boolean;
  warnings: string[] | null;
}

export interface SingleExplainResult {
  statement: string;
  is_explainable: boolean;
  note?: string;
  analyze_mode: boolean;
  summary: PlanSummary;
  raw_text?: string;
  safe_rollback: boolean;
}

export interface ExplainResponse {
  database_id: number;
  total_queries: number;
  results: SingleExplainResult[];
}

export type ApprovalDecision = 'pending' | 'approved' | 'rejected';

export type ApprovalRequestStatus =
  | 'pending_review'
  | 'approved'
  | 'rejected'
  | 'consumed'
  | 'superseded';

export interface GroupApprovalItem {
  group_id: number;
  group_name: string;
  access_level: AccessLevel;
  leader_user_id: string;
  leader_username: string;
  decision: ApprovalDecision;
  comment: string;
  decided_at: string | null;
}

export interface ApprovalRequest {
  id: number;
  database_id: number;
  database_name?: string;
  migration_id: number;
  migration_version: number;
  migration_revision: number;
  migration_name: string;
  action: MigrationAction;
  target_execution_id?: number | null;
  requested_by: string;
  note: string;
  status: ApprovalRequestStatus;
  required_approvals: number;
  approved_count: number;
  can_current_user_decide: boolean;
  approvals: GroupApprovalItem[];
  created_at: string;
  updated_at: string;
}

export interface ApiErrorPayload {
  error?: string;
  code?: string;
  detail?: string;
  lock_state?: string;
  blocking_execution_id?: number;
}
