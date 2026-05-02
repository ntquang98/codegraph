package sample

import "fmt"

// OrderService handles order business logic.
type OrderService struct {
	repo     Repository
	logger   Logger
	notifier Notifier
}

// NewOrderService creates a new OrderService.
func NewOrderService(repo Repository, logger Logger, notifier Notifier) *OrderService {
	return &OrderService{repo: repo, logger: logger, notifier: notifier}
}

// PlaceOrder creates a new order for the given user and products.
func (s *OrderService) PlaceOrder(userID int, productIDs []int) (*Order, error) {
	user, err := s.repo.FindUser(userID)
	if err != nil {
		s.logger.Error("find user failed", err)
		return nil, fmt.Errorf("find user: %w", err)
	}

	if !user.IsActive() {
		return nil, fmt.Errorf("user %d is not active", userID)
	}

	products, err := s.fetchProducts(productIDs)
	if err != nil {
		return nil, err
	}

	if err := s.checkStock(products); err != nil {
		return nil, err
	}

	order := s.buildOrder(user, products)
	if err := ValidateOrder(order); err != nil {
		return nil, fmt.Errorf("invalid order: %w", err)
	}

	if err := s.repo.SaveOrder(order); err != nil {
		s.logger.Error("save order failed", err)
		return nil, fmt.Errorf("save order: %w", err)
	}

	s.logger.Info("order placed successfully")
	if err := s.notifier.NotifyUser(userID, "Your order has been placed"); err != nil {
		s.logger.Warn("failed to notify user")
	}
	return order, nil
}

// CancelOrder cancels an existing order.
func (s *OrderService) CancelOrder(orderID int) error {
	orders, err := s.repo.ListOrders(0)
	if err != nil {
		return fmt.Errorf("list orders: %w", err)
	}

	var target *Order
	for _, o := range orders {
		if o.ID == orderID {
			target = o
			break
		}
	}

	if target == nil {
		return fmt.Errorf("order %d not found", orderID)
	}

	if !target.IsCancellable() {
		return fmt.Errorf("order %d cannot be cancelled in status %s", orderID, target.Status)
	}

	if err := s.repo.UpdateOrderStatus(orderID, OrderStatusCancelled); err != nil {
		s.logger.Error("cancel order failed", err)
		return fmt.Errorf("cancel order: %w", err)
	}

	s.logger.Info("order cancelled")
	if err := s.notifier.NotifyUser(target.UserID, "Your order has been cancelled"); err != nil {
		s.logger.Warn("failed to notify user on cancel")
	}
	return nil
}

// GetOrderSummary returns a formatted summary of an order.
func (s *OrderService) GetOrderSummary(orderID int) (string, error) {
	orders, err := s.repo.ListOrders(0)
	if err != nil {
		return "", fmt.Errorf("list orders: %w", err)
	}
	for _, o := range orders {
		if o.ID == orderID {
			return formatOrderSummary(o), nil
		}
	}
	return "", fmt.Errorf("order %d not found", orderID)
}

// fetchProducts retrieves all products by their IDs.
func (s *OrderService) fetchProducts(ids []int) ([]Product, error) {
	products := make([]Product, 0, len(ids))
	for _, id := range ids {
		p, err := s.repo.FindProduct(id)
		if err != nil {
			return nil, fmt.Errorf("find product %d: %w", id, err)
		}
		products = append(products, *p)
	}
	return products, nil
}

// checkStock verifies all products are available.
func (s *OrderService) checkStock(products []Product) error {
	for _, p := range products {
		if !p.IsAvailable() {
			return fmt.Errorf("product %d is out of stock", p.ID)
		}
	}
	return nil
}

// buildOrder assembles an Order from a user and products.
func (s *OrderService) buildOrder(user *User, products []Product) *Order {
	total := calculateTotal(products)
	return &Order{
		UserID:   user.ID,
		Products: products,
		Total:    total,
		Status:   OrderStatusPending,
	}
}

// calculateTotal sums the prices of all products.
func calculateTotal(products []Product) float64 {
	var total float64
	for _, p := range products {
		total += p.Price
	}
	return total
}

// ValidateOrder checks that an order is valid before processing.
func ValidateOrder(order *Order) error {
	if order == nil {
		return fmt.Errorf("order is nil")
	}
	if order.UserID <= 0 {
		return fmt.Errorf("invalid user ID: %d", order.UserID)
	}
	if len(order.Products) == 0 {
		return fmt.Errorf("order has no products")
	}
	if order.Total < 0 {
		return fmt.Errorf("order total cannot be negative")
	}
	return nil
}

// formatOrderSummary returns a human-readable summary of an order.
func formatOrderSummary(order *Order) string {
	return fmt.Sprintf("Order #%d: %d items, total $%.2f, status: %s",
		order.ID, order.ItemCount(), order.Total, order.Status)
}
