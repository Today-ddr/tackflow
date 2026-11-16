CREATE TRIGGER tag_delete_revoke_tokens AFTER DELETE ON tag BEGIN
  UPDATE api_token
  SET revoked_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
  WHERE revoked_at IS NULL
    AND EXISTS (
      SELECT 1 FROM json_each(api_token.tag_ids)
      WHERE value = OLD.id
    );
END;
