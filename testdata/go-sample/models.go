// Package sample provides a small Go sample for testing the code graph.
package sample

import "time"

// UserStatus represents the status of a user account.
type UserStatus int

const (
	UserStatusActive   UserStatus = iota
	UserStatusInactive UserStatus = iota
	UserStatusBanned   UserStatus = iota
)

// User represents a user in the system.
type User struct {
	ID        int
	Name      string
	Email     string
	Status    UserStatus
	CreatedAt time.Time
}

// IsActive returns true if the user account is active.
func (u *User) IsActive() bool {
	return u.Status == UserStatusActive
}

// DisplayName returns a formatted display name for the user.
func (u *User) DisplayName() string {
	return u.Name + " <" + u.Email + ">"
}

// Product represents a product in the catalog.
type Product struct {
	ID       int
	Name     string
	Price    float64
	Stock    int
	Category string
}

// IsAvailable returns true if the product is in stock.
func (p *Product) IsAvailable() bool {
	return p.Stock > 0
}

// DiscountedPrice returns the price after applying a discount percentage.
func (p *Product) DiscountedPrice(pct float64) float64 {
	return p.Price * (1 - pct/100)
}

// OrderStatus represents the status of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusConfirmed OrderStatus = "confirmed"
	OrderStatusShipped   OrderStatus = "shipped"
	OrderStatusDelivered OrderStatus = "delivered"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// Order represents a purchase order.
type Order struct {
	ID        int
	UserID    int
	Products  []Product
	Total     float64
	Status    OrderStatus
	CreatedAt time.Time
}

// IsCancellable returns true if the order can still be cancelled.
func (o *Order) IsCancellable() bool {
	return o.Status == OrderStatusPending || o.Status == OrderStatusConfirmed
}

// ItemCount returns the number of products in the order.
func (o *Order) ItemCount() int {
	return len(o.Products)
}

// Repository is the interface for data access.
type Repository interface {
	FindUser(id int) (*User, error)
	FindProduct(id int) (*Product, error)
	SaveOrder(order *Order) error
	ListOrders(userID int) ([]*Order, error)
	UpdateOrderStatus(orderID int, status OrderStatus) error
}

// Logger is the interface for logging.
type Logger interface {
	Info(msg string)
	Warn(msg string)
	Error(msg string, err error)
}

// Notifier is the interface for sending notifications.
type Notifier interface {
	NotifyUser(userID int, message string) error
	NotifyAdmin(message string) error
}
