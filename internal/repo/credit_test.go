package repo

import "testing"

func TestCreditAmountToApply(t *testing.T) {
	for _, test := range []struct{ balance, due, want int64 }{
		{150, 300, 150}, {500, 300, 300}, {-100, 300, 0}, {300, 0, 0}, {0, 300, 0},
	} {
		if got := creditAmountToApply(test.balance, test.due); got != test.want {
			t.Errorf("creditAmountToApply(%d, %d) = %d, want %d", test.balance, test.due, got, test.want)
		}
	}
}

func TestPaymentCanUseCredits(t *testing.T) {
	payment := Payment{Status: PaymentStatusPending, AmountCents: 500}
	if !paymentCanUseCredits(payment, 0, false) {
		t.Fatal("new unpaid invoice rejected")
	}
	url := "https://pay.example/checkout"
	payment.CheckoutURL = &url
	if paymentCanUseCredits(payment, 0, false) {
		t.Fatal("issued checkout accepted for repricing")
	}
	payment.CheckoutURL = nil
	if paymentCanUseCredits(payment, 200, false) {
		t.Fatal("already credited invoice accepted again")
	}
	if paymentCanUseCredits(payment, 0, true) {
		t.Fatal("checkout in preparation accepted")
	}
}
