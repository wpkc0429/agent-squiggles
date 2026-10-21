from .payments import Receipt, charge

PLANS = {"basic": 500, "pro": 2000}


def renew(customer_id: str, plan: str) -> Receipt:
    """Charges the plan's monthly price."""
    return charge(PLANS[plan], customer_id)
