use argon2::password_hash::{PasswordHash, PasswordHasher, PasswordVerifier, SaltString};
use argon2::{Algorithm, Argon2, Params, Version};
use password_hash::rand_core::OsRng;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone)]
pub struct PasswordPolicy {
    memory_kib: u32,
    iterations: u32,
    parallelism: u32,
}

impl PasswordPolicy {
    /// Tạo chính sách Argon2id với bộ nhớ tính bằng KiB.
    ///
    /// Trả `Config` nếu bộ nhớ dưới 19.456 KiB, số vòng lặp dưới 2, độ song song dưới 1
    /// hoặc bộ tham số không hợp lệ với Argon2.
    pub fn new(memory_kib: u32, iterations: u32, parallelism: u32) -> AppResult<Self> {
        if memory_kib < 19_456 {
            return Err(AppError::config("ARGON2_MEMORY_KIB must be >= 19456"));
        }
        if iterations < 2 {
            return Err(AppError::config("ARGON2_ITERATIONS must be >= 2"));
        }
        if parallelism < 1 {
            return Err(AppError::config("ARGON2_PARALLELISM must be >= 1"));
        }
        let _ = Params::new(memory_kib, iterations, parallelism, None)
            .map_err(|_| AppError::config("invalid Argon2 params"))?;
        Ok(Self {
            memory_kib,
            iterations,
            parallelism,
        })
    }

    fn hasher(&self) -> AppResult<Argon2<'static>> {
        let params = Params::new(self.memory_kib, self.iterations, self.parallelism, None)
            .map_err(|_| AppError::Internal)?;
        Ok(Argon2::new(Algorithm::Argon2id, Version::V0x13, params))
    }

    /// Băm mật khẩu bằng Argon2id với salt ngẫu nhiên và trả chuỗi PHC.
    ///
    /// Chấp nhận độ dài từ 8 đến 128 byte, kể cả hai đầu; ngoài khoảng này trả
    /// `BadRequest`. Lỗi tạo bộ băm hoặc băm mật khẩu được trả dưới dạng `Internal`.
    pub fn hash(&self, password: &str) -> AppResult<String> {
        if !(8..=128).contains(&password.len()) {
            return Err(AppError::BadRequest(
                "password length must be between 8 and 128".into(),
            ));
        }
        let salt = SaltString::generate(&mut OsRng);
        let hash = self
            .hasher()?
            .hash_password(password.as_bytes(), &salt)
            .map_err(|_| AppError::Internal)?
            .to_string();
        Ok(hash)
    }

    /// Đối chiếu mật khẩu với chuỗi PHC và trả `Ok(true)` khi khớp.
    ///
    /// Mật khẩu sai, hash không hợp lệ và lỗi xác minh đều được chuyển thành
    /// `Ok(false)`; hàm không kiểm tra giới hạn độ dài như khi tạo hash.
    pub fn verify(&self, password: &str, password_hash: &str) -> AppResult<bool> {
        let parsed = match PasswordHash::new(password_hash) {
            Ok(p) => p,
            Err(_) => return Ok(false),
        };
        let hasher = match self.hasher() {
            Ok(h) => h,
            Err(_) => return Ok(false),
        };
        match hasher.verify_password(password.as_bytes(), &parsed) {
            Ok(()) => Ok(true),
            Err(password_hash::Error::Password) => Ok(false),
            Err(_) => Ok(false),
        }
    }

    /// Trả `true` khi chuỗi PHC không đọc được, thuật toán khác Argon2id
    /// hoặc các tham số bộ nhớ, vòng lặp, độ song song khác chính sách hiện tại.
    pub fn needs_rehash(&self, password_hash: &str) -> bool {
        let Ok(parsed) = PasswordHash::new(password_hash) else {
            return true;
        };
        if parsed.algorithm.as_str() != "argon2id" {
            return true;
        }
        let m = parsed
            .params
            .get("m")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        let t = parsed
            .params
            .get("t")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        let p = parsed
            .params
            .get("p")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        m != self.memory_kib || t != self.iterations || p != self.parallelism
    }
}

impl PasswordPolicy {
    /// Thực hiện xác minh mật khẩu giả để giảm khác biệt thời gian khi đăng nhập.
    ///
    /// Bỏ qua kết quả và lỗi xác minh; không bảo đảm thời gian chạy cố định.
    pub fn dummy_verify(&self) {
        let hash = format!(
            "$argon2id$v=19$m={},t={},p=1$c29tZXNhbHRzb21lc2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            self.memory_kib,
            self.iterations
        );
        let Ok(parsed) = PasswordHash::new(&hash) else {
            return;
        };
        let Ok(hasher) = self.hasher() else {
            return;
        };
        let _ = hasher.verify_password(b"dummy-password-pad", &parsed);
    }
}
