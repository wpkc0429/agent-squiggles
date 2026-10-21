use crate::config::parse as load;

/// Reads the port from a command-line argument.
pub fn port_from_arg(arg: &str) -> u16 {
    load(arg).port
}
