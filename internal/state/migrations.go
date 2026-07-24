package state

const schemaV1 = `
	CREATE TABLE schema_version (version INTEGER NOT NULL);

	INSERT INTO schema_version(version) VALUES (1);

	CREATE TABLE projects (
  		path TEXT PRIMARY KEY,
   		name TEXT NOT NULL UNIQUE,
    	enabled INTEGER NOT NULL CHECK(enabled IN (0,1))
	);

	CREATE TABLE sessions (
  		thread_id TEXT PRIMARY KEY,
   		project_path TEXT NOT NULL REFERENCES projects(path),
    	active INTEGER NOT NULL CHECK(active IN (0,1)),
    	updated_at INTEGER NOT NULL
	);

	CREATE UNIQUE INDEX one_active_session_per_project ON sessions(project_path) WHERE active = 1;

	CREATE TABLE queued_messages (
  		id INTEGER PRIMARY KEY AUTOINCREMENT,
   		thread_id TEXT NOT NULL REFERENCES sessions(thread_id),
    	chat_id INTEGER NOT NULL,
    	text TEXT NOT NULL,
    	created_at INTEGER NOT NULL
	);

	CREATE TABLE bot_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);

	CREATE TABLE approvals (
  		nonce TEXT PRIMARY KEY,
   		request_id TEXT NOT NULL,
    	thread_id TEXT NOT NULL,
    	chat_id INTEGER NOT NULL,
    	kind TEXT NOT NULL,
    	expires_at INTEGER NOT NULL,
    	resolved INTEGER NOT NULL DEFAULT 0 CHECK(resolved IN (0,1)),
    	decision TEXT
     );
`

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{version: 1, sql: schemaV1},
}

func readSchemaVersion() int {
	return len(migrations)
}
