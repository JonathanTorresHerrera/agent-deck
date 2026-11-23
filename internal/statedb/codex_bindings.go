package statedb

import "encoding/json"

// CodexBindingRow is the part of an instance row the Codex notify writer
// needs to decide which instance a turn event belongs to.
type CodexBindingRow struct {
	ID             string
	Tool           string
	ProjectPath    string
	CodexSessionID string
	Archived       bool
	ToolData       json.RawMessage
}

// CodexBindingRows returns, read-only, every row whose tool_data binds
// codex_session_id to threadID, plus the row whose id is instanceID (when it
// exists), in one query. A row with malformed tool_data never matches the
// thread and never fails the query: CASE keeps json_extract off invalid JSON.
func (s *StateDB) CodexBindingRows(threadID, instanceID string) ([]CodexBindingRow, error) {
	rows, err := s.db.Query(`
		SELECT id, tool, project_path, archived_at, tool_data,
			CASE WHEN json_valid(tool_data)
				THEN COALESCE(json_extract(tool_data, '$.codex_session_id'), '')
				ELSE '' END
		FROM instances
		WHERE id = ?
			OR (? <> '' AND CASE WHEN json_valid(tool_data)
				THEN json_extract(tool_data, '$.codex_session_id') END = ?)
	`, instanceID, threadID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CodexBindingRow
	for rows.Next() {
		var r CodexBindingRow
		var archivedUnix int64
		var toolData string
		if err := rows.Scan(&r.ID, &r.Tool, &r.ProjectPath, &archivedUnix, &toolData, &r.CodexSessionID); err != nil {
			return nil, err
		}
		r.Archived = archivedUnix > 0
		r.ToolData = json.RawMessage(toolData)
		out = append(out, r)
	}
	return out, rows.Err()
}
