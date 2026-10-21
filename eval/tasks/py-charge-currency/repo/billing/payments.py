"""Payment processing."""
from dataclasses import dataclass


@dataclass
class Receipt:
    customer_id: str
    amount_cents: int


def charge(amount_cents: int, customer_id: str) -> Receipt:
    """Charges a customer and returns the receipt."""
    if amount_cents <= 0:
        raise ValueError("amount must be positive")
    return Receipt(customer_id=customer_id, amount_cents=amount_cents)
