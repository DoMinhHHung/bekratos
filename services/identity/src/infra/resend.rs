use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::client::conn::http1;
use hyper::Request;
use hyper_util::rt::TokioIo;
use rustls::pki_types::ServerName;
use rustls::{ClientConfig, RootCertStore};
use serde::Serialize;
use tokio::net::TcpStream;
use tokio::time::timeout;
use tokio_rustls::TlsConnector;
use webpki_roots::TLS_SERVER_ROOTS;

use crate::error::{AppError, AppResult};
use crate::infra::mailer::{Mailer, OutboundEmail};

const RESEND_HOST: &str = "api.resend.com";
const MAX_RESPONSE_BYTES: usize = 16 * 1024;

pub struct ResendMailer {
    from: String,
    api_key: String,
    timeout: Duration,
    tls_config: Arc<ClientConfig>,
}

#[derive(Serialize)]
struct ResendPayload<'a> {
    from: &'a str,
    to: [&'a str; 1],
    subject: &'a str,
    text: &'a str,
}

impl ResendMailer {
    pub fn new(from: String, api_key: String, timeout: Duration) -> Self {
        let mut roots = RootCertStore::empty();
        roots.extend(TLS_SERVER_ROOTS.iter().cloned());
        let tls_config = ClientConfig::builder()
            .with_root_certificates(roots)
            .with_no_client_auth();

        Self {
            from,
            api_key,
            timeout,
            tls_config: Arc::new(tls_config),
        }
    }

    async fn send_inner(&self, email: OutboundEmail) -> AppResult<()> {
        let tcp = TcpStream::connect((RESEND_HOST, 443))
            .await
            .map_err(|_| AppError::Unavailable)?;
        let server_name = ServerName::try_from(RESEND_HOST).map_err(|_| AppError::Unavailable)?;
        let tls = TlsConnector::from(self.tls_config.clone())
            .connect(server_name, tcp)
            .await
            .map_err(|_| AppError::Unavailable)?;

        let (mut sender, connection) = http1::handshake(TokioIo::new(tls))
            .await
            .map_err(|_| AppError::Unavailable)?;
        tokio::spawn(async move {
            let _ = connection.await;
        });

        let payload = ResendPayload {
            from: &self.from,
            to: [&email.to],
            subject: &email.subject,
            text: &email.text_body,
        };
        let body = serde_json::to_vec(&payload).map_err(|_| AppError::Internal)?;
        let request = Request::post("/emails")
            .header("host", RESEND_HOST)
            .header("authorization", format!("Bearer {}", self.api_key))
            .header("content-type", "application/json")
            .header("accept", "application/json")
            .body(Full::new(Bytes::from(body)))
            .map_err(|_| AppError::Unavailable)?;

        let mut response = sender
            .send_request(request)
            .await
            .map_err(|_| AppError::Unavailable)?;
        let accepted = response.status().is_success();
        let mut received = 0usize;

        while let Some(frame) = response.body_mut().frame().await {
            let frame = frame.map_err(|_| AppError::Unavailable)?;
            if let Some(data) = frame.data_ref() {
                received = received.saturating_add(data.len());
                if received > MAX_RESPONSE_BYTES {
                    return Err(AppError::Unavailable);
                }
            }
        }

        if accepted {
            Ok(())
        } else {
            Err(AppError::Unavailable)
        }
    }
}

#[async_trait]
impl Mailer for ResendMailer {
    async fn send(&self, email: OutboundEmail) -> AppResult<()> {
        timeout(self.timeout, self.send_inner(email))
            .await
            .map_err(|_| AppError::Unavailable)?
    }
}
