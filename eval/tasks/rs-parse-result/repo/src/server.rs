use crate::config::{self, Config};

/// Returns the address a server would bind to.
pub fn bind_address(spec: &str) -> String {
    let cfg: Config = config::parse(spec);
    format!("{}:{}", cfg.host, cfg.port)
}
