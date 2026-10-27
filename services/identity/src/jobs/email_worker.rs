use std::sync::Arc;
use std::time::Duration;

use chrono::{Duration as ChronoDuration, Utc};
use sqlx::PgPool;
use tokio::sync::watch;
use uuid::Uuid;

use crate::error::AppResult;
use crate::infra::mailer::{Mailer, OutboundEmail};
use crate::infra::tokens::MagicLinkSigner;
use crate::modules::token::repo::TokenRepo;

pub struct EmailWorkerConfig {
    pub poll_interval: Duration,
    pub batch_size: i64,
    pub lease_secs: i64,
    pub max_attempts: i32,
    pub public_base_url: String,
}

pub struct EmailWorker {
    pool: PgPool,
    mailer: Arc<dyn Mailer>,
    signer: MagicLinkSigner,
    cfg: EmailWorkerConfig,
}

impl EmailWorker {
    pub fn new(
        pool: PgPool,
        mailer: Arc<dyn Mailer>,
        signer: MagicLinkSigner,
        cfg: EmailWorkerConfig,
    ) -> Self {
        Self {
            pool,
            mailer,
            signer,
            cfg,
        }
    }

    /// Thử xử lý một lô email sau mỗi khoảng polling, tiếp tục khi một lượt lỗi
    /// và dừng khi nhận thay đổi giá trị shutdown thành `true`.
    pub async fn run(self, mut shutdown: watch::Receiver<bool>) {
        loop {
            tokio::select! {
                _ = shutdown.changed() => {
                    if *shutdown.borrow() {
                        tracing::info!("email worker shutting down");
                        break;
                    }
                }
                _ = tokio::time::sleep(self.cfg.poll_interval) => {
                    if let Err(e) = self.tick().await {
                        tracing::error!(error = %e, "email worker tick failed");
                    }
                }
            }
        }
    }

    async fn tick(&self) -> AppResult<()> {
        let repo = TokenRepo::new(&self.pool);
        let due = repo
            .claim_due_for_delivery(self.cfg.batch_size, self.cfg.lease_secs)
            .await?;

        for row in due {
            if row.delivery_attempts > self.cfg.max_attempts {
                repo.mark_delivery_permanent_failure(row.id, "max_attempts")
                    .await?;
                continue;
            }

            let link_token = match self.signer.issue(&row.purpose, row.id) {
                Ok(t) => t,
                Err(_) => {
                    if row.delivery_attempts >= self.cfg.max_attempts {
                        repo.mark_delivery_permanent_failure(row.id, "sign_failed")
                            .await?;
                    } else {
                        repo.mark_delivery_failure(row.id, "sign_failed", backoff_secs(row.delivery_attempts))
                            .await?;
                    }
                    continue;
                }
            };

            let (path, subject) = match row.purpose.as_str() {
                "email_verify" => ("verify-email", "Verify your email"),
                "password_reset" => ("reset-password", "Reset your password"),
                "account_restore" => ("restore-account", "Restore your account"),
                _ => {
                    repo.mark_delivery_permanent_failure(row.id, "unsupported_purpose")
                        .await?;
                    continue;
                }
            };
            let url = format!(
                "{}/{path}?token={link_token}",
                self.cfg.public_base_url.trim_end_matches('/')
            );
            let body = format!(
                "Open this link to continue. It expires at {}.\n\n{url}\n",
                row.expires_at
            );
            let email = OutboundEmail {
                to: row.email.clone(),
                subject: subject.to_string(),
                text_body: body,
            };

            match self.mailer.send(email).await {
                Ok(()) => {
                    repo.mark_sent(row.id).await?;
                }
                Err(_) => {
                    if row.delivery_attempts >= self.cfg.max_attempts {
                        repo.mark_delivery_permanent_failure(row.id, "send_failed")
                            .await?;
                    } else {
                        repo.mark_delivery_failure(
                            row.id,
                            "send_failed",
                            backoff_secs(row.delivery_attempts),
                        )
                        .await?;
                    }
                }
            }
        }
        Ok(())
    }
}

/// Trả thời gian chờ thử lại tính bằng giây: 30 giây nhân đôi theo số lần thử,
/// chặn số lần ở khoảng 1–8 và thời gian chờ tối đa 3.600 giây.
fn backoff_secs(attempts: i32) -> i64 {
    let exp = attempts.clamp(1, 8) as u32;
    let secs = 30u64.saturating_mul(2u64.saturating_pow(exp.saturating_sub(1)));
    secs.min(3600) as i64
}

/// Trả thời điểm UTC hiện tại cộng `hours` giờ; giá trị âm cho thời điểm quá khứ.
pub fn default_expiry(hours: i64) -> chrono::DateTime<Utc> {
    Utc::now() + ChronoDuration::hours(hours)
}

/// Sinh UUID v7 cho token mới.
pub fn new_token_id() -> Uuid {
    Uuid::now_v7()
}
