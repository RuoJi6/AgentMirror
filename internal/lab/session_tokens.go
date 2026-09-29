package lab

import "crypto/subtle"

// Only the latest token hash is mutable. Original session snapshots and actual
// delivery bodies remain historical evidence, never rewritten by rotation.
func saveSessionToken(q queryer, id, token string) {
	exec(q, `INSERT INTO session_tokens(session_id,token_hash,issued_at) VALUES(?,?,?)
ON CONFLICT(session_id) DO UPDATE SET token_hash=excluded.token_hash,issued_at=excluded.issued_at`, id, tokenHash(token), timestamp())
}

func rotateSessionToken(q queryer, session Doc) {
	token := randomToken(24)
	saveSessionToken(q, str(session["id"]), token)
	object(session["snapshot"])["token"] = token
}

func validSessionToken(q queryer, id string, snapshot Doc, token string) bool {
	if token == "" {
		return false
	}
	current := rows(q, "SELECT token_hash FROM session_tokens WHERE session_id=?", id)
	if len(current) == 0 {
		// Upgrades retain already-issued tokens until that session's next public
		// request. Once rotated, the historical token is never a fallback.
		return subtle.ConstantTimeCompare([]byte(str(snapshot["token"])), []byte(token)) == 1
	}
	return subtle.ConstantTimeCompare([]byte(str(current[0]["token_hash"])), []byte(tokenHash(token))) == 1
}
