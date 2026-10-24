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
    /// Chuyển email cho nhà cung cấp; trả lỗi của nhà cung cấp nếu không chấp nhận được.
    async fn send(&self, email: OutboundEmail) -> AppResult<()>;
}

pub struct ConsoleMailer {
    pub from: String,
}

#[async_trait]
impl Mailer for ConsoleMailer {
    /// Chấp nhận email cho chế độ console và trả thành công; không gửi email ra ngoài.
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
    /// Luôn trả `Unavailable`, để bên gọi xử lý như một lần gửi email thất bại.
    async fn send(&self, _email: OutboundEmail) -> AppResult<()> {
        Err(AppError::Unavailable)
    }
}
