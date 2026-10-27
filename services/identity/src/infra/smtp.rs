use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use rustls::pki_types::ServerName;
use rustls::{ClientConfig, RootCertStore};
use tokio::io::{AsyncReadExt, AsyncWriteExt, BufReader};
use tokio::net::TcpStream;
use tokio::time::timeout;
use tokio_rustls::TlsConnector;
use webpki_roots::TLS_SERVER_ROOTS;

use crate::error::{AppError, AppResult};
use crate::infra::mailer::{Mailer, OutboundEmail};

const MAX_RESPONSE_BYTES: usize = 16 * 1024;
const MAX_RESPONSE_LINE_BYTES: usize = 4096;
const MAX_RESPONSE_LINES: usize = 32;
const MAX_MESSAGE_BYTES: usize = 128 * 1024;

pub struct SmtpMailer {
    host: String,
    port: u16,
    username: String,
    app_password: String,
    from: String,
    timeout: Duration,
    tls_config: Arc<ClientConfig>,
}

impl SmtpMailer {
    pub fn new(
        host: String,
        port: u16,
        username: String,
        app_password: String,
        from: String,
        timeout: Duration,
    ) -> Self {
        let mut roots = RootCertStore::empty();
        roots.extend(TLS_SERVER_ROOTS.iter().cloned());
        let tls_config = ClientConfig::builder()
            .with_root_certificates(roots)
            .with_no_client_auth();

        Self {
            host,
            port,
            username,
            app_password,
            from,
            timeout,
            tls_config: Arc::new(tls_config),
        }
    }

    async fn send_inner(&self, email: OutboundEmail) -> AppResult<()> {
        validate_mailbox(&self.from)?;
        validate_mailbox(&email.to)?;
        if email.text_body.len() > MAX_MESSAGE_BYTES || email.subject.is_empty()
            || email.subject.len() > 998
            || email.subject.contains(['\r', '\n'])
            || !email.subject.is_ascii()
        {
            return Err(AppError::Unavailable);
        }

        let tcp = TcpStream::connect((self.host.as_str(), self.port))
            .await
            .map_err(|_| AppError::Unavailable)?;
        let server_name =
            ServerName::try_from(self.host.clone()).map_err(|_| AppError::Unavailable)?;
        let tls = TlsConnector::from(self.tls_config.clone())
            .connect(server_name, tcp)
            .await
            .map_err(|_| AppError::Unavailable)?;
        let mut stream = BufReader::new(tls);

        expect_response(&mut stream, &[220]).await?;
        command(&mut stream, "EHLO bekratos.invalid".as_str(), &[250]).await?;
        command(&mut stream, "AUTH LOGIN", &[334]).await?;
        command(&mut stream, STANDARD.encode(self.username.as_bytes()).as_str(), &[334]).await?;
        command(
            &mut stream,
            STANDARD.encode(self.app_password.as_bytes()).as_str(),
            &[235],
        )
        .await?;
        command(
            &mut stream,
            format!("MAIL FROM:<{}>", self.from).as_str(),
            &[250],
        )
        .await?;
        command(
            &mut stream,
            format!("RCPT TO:<{}>", email.to).as_str(),
            &[250, 251],
        )
        .await?;
        command(&mut stream, "DATA", &[354]).await?;

        let encoded_body = encode_body(&email.text_body);
        let message = format!(
            "From: <{}>\r\nTo: <{}>\r\nSubject: {}\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n{}\r\n",
            self.from, email.to, email.subject, encoded_body
        );
        if message.len() > MAX_MESSAGE_BYTES {
            return Err(AppError::Unavailable);
        }
        stream
            .get_mut()
            .write_all(message.as_bytes())
            .await
            .map_err(|_| AppError::Unavailable)?;
        stream
            .get_mut()
            .write_all(b".\r\n")
            .await
            .map_err(|_| AppError::Unavailable)?;
        stream
            .get_mut()
            .flush()
            .await
            .map_err(|_| AppError::Unavailable)?;
        expect_response(&mut stream, &[250]).await?;
        let _ = command(&mut stream, "QUIT", &[221]).await;
        Ok(())
    }
}

#[async_trait]
impl Mailer for SmtpMailer {
    async fn send(&self, email: OutboundEmail) -> AppResult<()> {
        if self.host.is_empty()
            || self.username.trim().is_empty()
            || self.app_password.trim().is_empty()
            || self.from.trim().is_empty()
        {
            return Err(AppError::Unavailable);
        }
        timeout(self.timeout, self.send_inner(email))
            .await
            .map_err(|_| AppError::Unavailable)?
    }
}

async fn command(
    stream: &mut BufReader<tokio_rustls::client::TlsStream<TcpStream>>,
    command: &str,
    accepted: &[u16],
) -> AppResult<()> {
    if command.contains(['\r', '\n']) || command.len() > 4096 {
        return Err(AppError::Unavailable);
    }
    stream
        .get_mut()
        .write_all(command.as_bytes())
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .write_all(b"\r\n")
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .flush()
        .await
        .map_err(|_| AppError::Unavailable)?;
    expect_response(stream, accepted).await
}

async fn expect_response(
    stream: &mut BufReader<tokio_rustls::client::TlsStream<TcpStream>>,
    accepted: &[u16],
) -> AppResult<()> {
    let mut total = 0usize;
    for _ in 0..MAX_RESPONSE_LINES {
        let mut line = Vec::with_capacity(128);
        loop {
            let byte = stream.read_u8().await.map_err(|_| AppError::Unavailable)?;
            line.push(byte);
            total = total.saturating_add(1);
            if line.len() > MAX_RESPONSE_LINE_BYTES || total > MAX_RESPONSE_BYTES {
                return Err(AppError::Unavailable);
            }
            if byte == b'\n' {
                break;
            }
        }
        if line.len() < 4 || !line[..3].iter().all(u8::is_ascii_digit) {
            return Err(AppError::Unavailable);
        }
        let code = u16::from(line[0] - b'0') * 100
            + u16::from(line[1] - b'0') * 10
            + u16::from(line[2] - b'0');
        if line[3] == b'-' {
            continue;
        }
        if accepted.contains(&code) {
            return Ok(());
        }
        return Err(AppError::Unavailable);
    }
    Err(AppError::Unavailable)
}

fn validate_mailbox(value: &str) -> AppResult<()> {
    let mut parts = value.split('@');
    let local = parts.next().unwrap_or_default();
    let domain = parts.next().unwrap_or_default();
    if local.is_empty()
        || domain.is_empty()
        || parts.next().is_some()
        || value.len() > 254
        || value.bytes().any(|b| b.is_ascii_control() || b.is_ascii_whitespace() || matches!(b, b'<' | b'>'))
    {
        return Err(AppError::Unavailable);
    }
    Ok(())
}

fn encode_body(body: &str) -> String {
    STANDARD
        .encode(body.as_bytes())
        .as_bytes()
        .chunks(76)
        .map(|line| std::str::from_utf8(line).unwrap_or_default())
        .collect::<Vec<_>>()
        .join("\r\n")
}

#[cfg(test)]
mod tests {
    use super::{encode_body, validate_mailbox};

    #[test]
    fn validates_mailboxes_without_header_or_command_injection() {
        assert!(validate_mailbox("user@example.com").is_ok());
        assert!(validate_mailbox("user@example.com\r\nRCPT TO:<attacker@example.com>").is_err());
        assert!(validate_mailbox("user@example.com\n").is_err());
        assert!(validate_mailbox("not-an-email").is_err());
    }

    #[test]
    fn encodes_utf8_body_as_bounded_mime_lines() {
        let encoded = encode_body("magic link https://example.com/verify?token=abc");
        assert!(encoded.is_ascii());
        assert!(encoded.lines().all(|line| line.len() <= 76));
    }
}
