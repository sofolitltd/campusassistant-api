package domain

import (
	"context"

	"github.com/google/uuid"
)

// OrderStatus represents the lifecycle of a marketplace order.
type OrderStatus string

const (
	OrderStatusPendingPayment OrderStatus = "pending_payment"
	OrderStatusPaid           OrderStatus = "paid"
	OrderStatusProcessing     OrderStatus = "processing"
	OrderStatusShipped        OrderStatus = "shipped"
	OrderStatusDelivered      OrderStatus = "delivered"
	OrderStatusCancelled      OrderStatus = "cancelled"
)

// PaymentMethod is how the buyer chose to settle an order at checkout.
type PaymentMethod string

const (
	PaymentMethodBkash          PaymentMethod = "bkash"
	PaymentMethodCashOnDelivery PaymentMethod = "cash_on_delivery"
)

// Order is a marketplace purchase. Shipping fields are snapshotted from the
// buyer's selected Address at checkout time (not a live FK), so a later
// address edit/delete never mutates order history.
type Order struct {
	Base
	BuyerID               uuid.UUID     `gorm:"type:uuid;not null;index" json:"buyer_id"`
	ShippingRecipientName string        `gorm:"size:255;not null" json:"shipping_recipient_name"`
	ShippingPhone         string        `gorm:"size:20;not null" json:"shipping_phone"`
	ShippingAddressLine   string        `gorm:"size:500;not null" json:"shipping_address_line"`
	ShippingCity          string        `gorm:"size:100;not null" json:"shipping_city"`
	TotalAmount           int           `gorm:"not null" json:"total_amount"` // poisha
	Status                OrderStatus   `gorm:"type:varchar(20);default:'pending_payment';index" json:"status"`
	PaymentMethod         PaymentMethod `gorm:"type:varchar(20);default:'bkash'" json:"payment_method"`
	Items                 []OrderItem   `gorm:"foreignKey:OrderID;constraint:OnDelete:CASCADE" json:"items,omitempty"`
}

// OrderItem is a single line-item within an Order. ProductTitle/UnitPrice
// are snapshots, so product edits/deletion never alter order history.
type OrderItem struct {
	Base
	OrderID                uuid.UUID `gorm:"type:uuid;not null;index" json:"order_id"`
	ProductID              uuid.UUID `gorm:"type:uuid;not null" json:"product_id"`
	ProductTitle           string    `gorm:"size:255;not null" json:"product_title"`
	MerchantID             uuid.UUID `gorm:"type:uuid;index" json:"merchant_id"`
	Quantity               int       `gorm:"not null" json:"quantity"`
	UnitPrice              int       `gorm:"not null" json:"unit_price"` // poisha
	CommissionRateSnapshot float64   `gorm:"default:0" json:"commission_rate_snapshot"`
}

// OrderRepository handles CRUD and status transitions for marketplace orders.
type OrderRepository interface {
	Create(ctx context.Context, order *Order) error
	GetByID(ctx context.Context, id uuid.UUID) (*Order, error)
	GetByBuyer(ctx context.Context, buyerID uuid.UUID) ([]Order, error)
	GetAll(ctx context.Context, status OrderStatus) ([]Order, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status OrderStatus) error
	// GetByMerchant returns every order containing at least one item sold by
	// merchantID — a merchant's own "my orders" view, not the full order
	// (other merchants' items in the same checkout are excluded by the
	// caller before serializing).
	GetByMerchant(ctx context.Context, merchantID uuid.UUID) ([]Order, error)
}
