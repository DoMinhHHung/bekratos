use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use http::header::{AUTHORIZATION, CONTENT_LENGTH, CONTENT_TYPE, CONNECTION, HOST};
use http::{Request, Uri};
use http_body_util::{BodyExt, Full, Limited};
use hyper::client::conn::http1;
use hyper_util::rt::TokioIo;
use rustls::pki_types::ServerName;
use rustls::{ClientConfig, RootCertStore};
use serde_json::json;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::TcpStream;
use tokio_rustls::TlsConnector;

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
    async fn send(&self, _email: OutboundEmail) -> AppResult<()> {
        tracing::info!(provider = "console", from = %self.from, "email delivery simulated");
        Ok(())
    }
}

pub struct ResendMailer {
    pub from: String,
    pub api_key: String,
    pub api_base_url: String,
    pub timeout: Duration,
}

impl ResendMailer {
    pub fn new(from: String, api_key: String, api_base_url: String, timeout: Duration) -> Self {
        Self {
            from,
            api_key,
            api_base_url: api_base_url.trim_end_matches('/').to_string(),
            timeout,
        }
    }

    async fn send_inner(&self, email: OutboundEmail) -> AppResult<()> {
        let endpoint = format!("{}/emails", self.api_base_url);
        let uri: Uri = endpoint.parse().map_err(|_| AppError::Unavailable)?;
        let scheme = uri.scheme_str().ok_or(AppError::Unavailable)?;
        let host = uri.host().ok_or(AppError::Unavailable)?;
        let port = uri.port_u16().unwrap_or(if scheme == "https" { 443 } else { 80 });
        if !matches!(scheme, "https" | "http") {
            return Err(AppError::Unavailable);
        }

        let stream = TcpStream::connect((host, port))
            .await
            .map_err(|_| AppError::Unavailable)?;
        let io: Box<dyn ClientIo> = if scheme == "https" {
            let roots = RootCertStore::from_iter(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
            let tls_config = ClientConfig::builder()
                .with_root_certificates(roots)
                .with_no_client_auth();
            let server_name =
                ServerName::try_from(host.to_string()).map_err(|_| AppError::Unavailable)?;
            let connector = TlsConnector::from(Arc::new(tls_config));
            Box::new(
                connector
                    .connect(server_name, stream)
                    .await
                    .map_err(|_| AppError::Unavailable)?,
            )
        } else {
            Box::new(stream)
        };

        let (mut sender, connection) =
            http1::handshake::<_, Full<Bytes>>(TokioIo::new(io))
                .await
                .map_err(|_| AppError::Unavailable)?;
        tokio::spawn(async move {
            if connection.await.is_err() {
                tracing::debug!("mail provider HTTP connection closed");
            }
        });

        let payload = serde_json::to_vec(&json!({
            "from": &self.from,
            "to": [email.to],
            "subject": email.subject,
            "text": email.text_body
        }))
        .map_err(|_| AppError::Unavailable)?;
        let host_header = uri.authority().ok_or(AppError::Unavailable)?.as_str();
        let path = uri.path_and_query().map(|value| value.as_str()).unwrap_or("/");
        let authorization = format!("Bearer {}", self.api_key);
        let request = Request::builder()
            .method(http::Method::POST)
            .uri(path)
            .header(HOST, host_header)
            .header(AUTHORIZATION, authorization)
            .header(CONTENT_TYPE, "application/json")
            .header(CONTENT_LENGTH, payload.len())
            .header(CONNECTION, "close")
            .body(Full::new(Bytes::from(payload)))
            .map_err(|_| AppError::Unavailable)?;

        let response = sender
            .send_request(request)
            .await
            .map_err(|_| AppError::Unavailable)?;
        let status = response.status();
        Limited::new(response.into_body(), 64 * 1024)
            .collect()
            .await
            .map_err(|_| AppError::Unavailable)?;

        if !status.is_success() {
            tracing::warn!(provider_status = status.as_u16(), "mail provider rejected delivery");
            return Err(AppError::Unavailable);
        }

        Ok(())
    }
}

trait ClientIo: AsyncRead + AsyncWrite + Unpin + Send {}

impl<T> ClientIo for T where T: AsyncRead + AsyncWrite + Unpin + Send {}

#[async_trait]
impl Mailer for ResendMailer {
    async fn send(&self, email: OutboundEmail) -> AppResult<()> {
        match tokio::time::timeout(self.timeout, self.send_inner(email)).await {
            Ok(result) => result,
            Err(_) => {
                tracing::warn!("mail provider request timed out");
                Err(AppError::Unavailable)
            }
        }
    }
}

pub struct NoopMailer;

#[async_trait]
impl Mailer for NoopMailer {
    async fn send(&self, _email: OutboundEmail) -> AppResult<()> {
        Err(AppError::Unavailable)
    }
}


#[cfg(test)]
mod tests {
    use super::{Mailer, OutboundEmail, ResendMailer};
    use std::sync::Arc;
    use std::time::Duration;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;

    #[tokio::test]
    async fn resend_mailer_posts_email_and_accepts_provider_success() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let captured = Arc::new(tokio::sync::Mutex::new(String::new()));
        let captured_server = Arc::clone(&captured);
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = Vec::new();
            let mut chunk = [0_u8; 2048];
            loop {
                let count = stream.read(&mut chunk).await.unwrap();
                assert!(count > 0, "client closed before completing request");
                request.extend_from_slice(&chunk[..count]);
                if let Some((body_start, body_len)) = request_body_bounds(&request) {
                    if request.len() >= body_start + body_len {
                        break;
                    }
                }
            }
            *captured_server.lock().await = String::from_utf8_lossy(&request).to_string();
            stream
                .write_all(b"HTTP/1.1 202 Accepted\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
                .await
                .unwrap();
        });

        let mailer = ResendMailer::new(
            "Bekratos <noreply@example.test>".to_string(),
            "synthetic-resend-api-key-0123456789".to_string(),
            format!("http://{address}"),
            Duration::from_secs(2),
        );
        mailer
            .send(OutboundEmail {
                to: "user@example.test".to_string(),
                subject: "Verify your email".to_string(),
                text_body: "Open the single-use verification link".to_string(),
            })
            .await
            .unwrap();
        server.await.unwrap();

        let request = captured.lock().await.to_ascii_lowercase();
        assert!(request.starts_with("post /emails http/1.1"));
        assert!(request.contains("authorization: bearer synthetic-resend-api-key-0123456789"));
        assert!(request.contains("user@example.test"));
        assert!(request.contains("verify your email"));
    }

    fn request_body_bounds(request: &[u8]) -> Option<(usize, usize)> {
        let separator = request.windows(4).position(|window| window == b"\r\n\r\n")?;
        let headers = String::from_utf8_lossy(&request[..separator]);
        let content_length = headers.lines().find_map(|line| {
            let (name, value) = line.split_once(':')?;
            if name.eq_ignore_ascii_case("content-length") {
                value.trim().parse::<usize>().ok()
            } else {
                None
            }
        })?;
        Some((separator + 4, content_length))
    }
}
