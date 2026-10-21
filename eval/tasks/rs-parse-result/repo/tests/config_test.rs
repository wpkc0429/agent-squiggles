use conf::config::parse;

#[test]
fn parses_host_and_port() {
    let cfg = parse("example.com:443");
    assert_eq!(cfg.host, "example.com");
    assert_eq!(cfg.port, 443);
}
