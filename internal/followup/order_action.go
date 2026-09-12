package followup

import "time"

type OrderAction struct {
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	Email      string    `json:"email"`
	DueAt      time.Time `json:"due_at"`
}

type Checkout struct {
	OrderID    string `json:"order_id"`
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	DelayHours int    `json:"delay_hours"`
}

func NewOrderAction(checkout Checkout, now time.Time) OrderAction {
	return OrderAction{
		OrderID:    checkout.OrderID,
		CustomerID: checkout.CustomerID,
		Email:      checkout.Email,
		DueAt:      now.UTC().Add(time.Duration(checkout.DelayHours) * time.Hour),
	}
}

func Ready(action OrderAction, now time.Time) bool {
	return !now.Before(action.DueAt)
}
