use redis::aio::ConnectionManager;
use redis::Script;
use sha2::{Digest, Sha256};

use crate::error::{AppError, AppResult};

const INCR_WITH_EXPIRE: &str = r#"
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return current
"#;

#[derive(Clone)]
pub struct RateLimiter {
    conn: ConnectionManager,
}

impl RateLimiter {
    pub fn new(conn: ConnectionManager) -> Self {
        Self { conn }
    }

    fn hash_id(raw: &str) -> String {
        let mut hasher = Sha256::new();
        hasher.update(raw.as_bytes());
        hex::encode(hasher.finalize())
    }

    pub async fn check_and_incr(
        &self,
        endpoint: &str,
        dimension: &str,
        id: &str,
        limit: u64,
        window_secs: u64,
    ) -> AppResult<()> {
        let key = format!(
            "rate_limit:{}:{}:{}",
            endpoint,
            dimension,
            Self::hash_id(id)
        );

        let mut conn = self.conn.clone();
        let count: u64 = Script::new(INCR_WITH_EXPIRE)
            .key(&key)
            .arg(window_secs)
            .invoke_async(&mut conn)
            .await
            .map_err(|e| {
                tracing::error!(error = %e, "rate limit incr failed");
                AppError::Unavailable
            })?;

        if count > limit {
            return Err(AppError::TooManyRequests);
        }

        Ok(())
    }
}
