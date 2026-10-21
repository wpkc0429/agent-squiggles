/// Server configuration.
#[derive(Debug, Clone, PartialEq)]
pub struct Config {
    pub host: String,
    pub port: u16,
}

/// Parses "host:port". Panics on invalid input.
pub fn parse(input: &str) -> Config {
    let (host, port) = input.split_once(':').expect("expected host:port");
    Config {
        host: host.to_string(),
        port: port.parse().expect("invalid port"),
    }
}
