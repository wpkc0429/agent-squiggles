from billing import payments


def verify_card(customer_id: str) -> int:
    """Charges a token amount to verify a card before issuing a credit."""
    return payments.charge(1, customer_id).amount_cents
