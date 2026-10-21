import unittest

from billing.payments import charge
from billing.subscriptions import renew
from shop.checkout import pay_order
from shop.refunds import verify_card


class BillingTest(unittest.TestCase):
    def test_charge(self):
        self.assertEqual(charge(250, "c1").amount_cents, 250)

    def test_charge_rejects_zero(self):
        with self.assertRaises(ValueError):
            charge(0, "c1")

    def test_renew(self):
        self.assertEqual(renew("c2", "pro").amount_cents, 2000)

    def test_pay_order(self):
        self.assertEqual(pay_order("c3", [100, 250]), 350)

    def test_verify_card(self):
        self.assertEqual(verify_card("c4"), 1)


if __name__ == "__main__":
    unittest.main()
