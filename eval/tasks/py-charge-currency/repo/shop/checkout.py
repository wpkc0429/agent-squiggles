from typing import List

from billing.payments import charge as pay


def pay_order(customer_id: str, line_cents: List[int]) -> int:
    """Charges the sum of the order lines and returns the amount charged."""
    receipt = pay(sum(line_cents), customer_id)
    return receipt.amount_cents
