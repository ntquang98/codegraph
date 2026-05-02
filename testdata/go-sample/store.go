package sample

import (
	"fmt"
	"sync"
	"time"
)

// InMemoryStore is an in-memory implementation of Repository.
type InMemoryStore struct {
	mu       sync.RWMutex
	users    map[int]*User
	products map[int]*Product
	orders   map[int]*Order
	nextID   int
}

// NewInMemoryStore creates a new InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		users:    make(map[int]*User),
		products: make(map[int]*Product),
		orders:   make(map[int]*Order),
		nextID:   1,
	}
}

// FindUser retrieves a user by ID.
func (s *InMemoryStore) FindUser(id int) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, fmt.Errorf("user %d not found", id)
	}
	return u, nil
}

// FindProduct retrieves a product by ID.
func (s *InMemoryStore) FindProduct(id int) (*Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.products[id]
	if !ok {
		return nil, fmt.Errorf("product %d not found", id)
	}
	return p, nil
}

// SaveOrder persists an order.
func (s *InMemoryStore) SaveOrder(order *Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if order.ID == 0 {
		order.ID = s.nextID
		s.nextID++
	}
	order.CreatedAt = time.Now()
	s.orders[order.ID] = order
	return nil
}

// ListOrders returns all orders, optionally filtered by userID (0 = all).
func (s *InMemoryStore) ListOrders(userID int) ([]*Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Order, 0, len(s.orders))
	for _, o := range s.orders {
		if userID == 0 || o.UserID == userID {
			result = append(result, o)
		}
	}
	return result, nil
}

// UpdateOrderStatus changes the status of an order.
func (s *InMemoryStore) UpdateOrderStatus(orderID int, status OrderStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return fmt.Errorf("order %d not found", orderID)
	}
	o.Status = status
	return nil
}

// AddUser inserts a user into the store.
func (s *InMemoryStore) AddUser(u *User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = u
}

// AddProduct inserts a product into the store.
func (s *InMemoryStore) AddProduct(p *Product) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.products[p.ID] = p
}

// CountOrders returns the number of stored orders.
func (s *InMemoryStore) CountOrders() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.orders)
}

// ConsoleLogger is a simple logger that prints to stdout.
type ConsoleLogger struct{}

// Info logs an informational message.
func (l *ConsoleLogger) Info(msg string) {
	fmt.Println("[INFO]", msg)
}

// Warn logs a warning message.
func (l *ConsoleLogger) Warn(msg string) {
	fmt.Println("[WARN]", msg)
}

// Error logs an error message.
func (l *ConsoleLogger) Error(msg string, err error) {
	fmt.Printf("[ERROR] %s: %v\n", msg, err)
}

// NoopNotifier is a notifier that does nothing.
type NoopNotifier struct{}

// NotifyUser sends a notification to a user (no-op).
func (n *NoopNotifier) NotifyUser(userID int, message string) error {
	return nil
}

// NotifyAdmin sends a notification to an admin (no-op).
func (n *NoopNotifier) NotifyAdmin(message string) error {
	return nil
}
