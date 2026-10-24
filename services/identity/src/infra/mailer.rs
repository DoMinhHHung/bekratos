use async_trait::async_trait;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone)]
pub struct OutboundEmail {
    pub to: String,
    pub subject: String,
    pub text_body: String,
}

#[async_trait]
pub trait Mailer: Send + Sync {
    async fn send(&self, email: OutboundEmail) -> AppResult<()>;
}

pub struct ConsoleMailer {
    pub from: String,
}

#[async_trait]
impl Mailer for ConsoleMailer {
    async fn send(&self, email: OutboundEmail) -> AppResult<()> {
        tracing::info!(
            from = %self.from,
            to = %email.to,
            subject = %email.subject,
            "console mailer: email accepted"
        );
        Ok(())
    }
}

pub struct NoopMailer;

#[async_trait]
impl Mailer for NoopMailer {
    async fn send(&self, _email: OutboundEmail) -> AppResult<()> {
        Err(AppError::Unavailable)
    }
}
