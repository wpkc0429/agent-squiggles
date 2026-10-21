fn main() {
    let spec = std::env::args()
        .nth(1)
        .unwrap_or_else(|| "localhost:8080".to_string());
    println!("{}", conf::server::bind_address(&spec));
}
