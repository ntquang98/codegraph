// models.rs — Rust sample for code-graph test fixtures.
// Provides structs, enums, traits, impl blocks, and use declarations.
// Must produce at least 10 symbols.

use std::fmt;
use std::collections::HashMap;

/// Status of a user account.
#[derive(Debug, Clone, PartialEq)]
pub enum UserStatus {
    Active,
    Inactive,
    Banned,
}

/// A user in the system.
#[derive(Debug, Clone)]
pub struct User {
    pub id: u64,
    pub name: String,
    pub email: String,
    pub status: UserStatus,
}

impl User {
    /// Create a new active user.
    pub fn new(id: u64, name: String, email: String) -> Self {
        User {
            id,
            name,
            email,
            status: UserStatus::Active,
        }
    }

    /// Returns true if the user account is active.
    pub fn is_active(&self) -> bool {
        self.status == UserStatus::Active
    }

    /// Returns a formatted display name.
    pub fn display_name(&self) -> String {
        format!("{} <{}>", self.name, self.email)
    }
}

/// A product in the catalog.
#[derive(Debug, Clone)]
pub struct Product {
    pub id: u64,
    pub name: String,
    pub price: f64,
    pub stock: u32,
}

impl Product {
    /// Returns true if the product is in stock.
    pub fn is_available(&self) -> bool {
        self.stock > 0
    }

    /// Returns the discounted price.
    pub fn discounted_price(&self, pct: f64) -> f64 {
        self.price * (1.0 - pct / 100.0)
    }
}

/// Status of an order.
#[derive(Debug, Clone, PartialEq)]
pub enum OrderStatus {
    Pending,
    Confirmed,
    Shipped,
    Delivered,
    Cancelled,
}

/// A purchase order.
#[derive(Debug, Clone)]
pub struct Order {
    pub id: u64,
    pub user_id: u64,
    pub products: Vec<Product>,
    pub total: f64,
    pub status: OrderStatus,
}

impl Order {
    /// Returns true if the order can still be cancelled.
    pub fn is_cancellable(&self) -> bool {
        self.status == OrderStatus::Pending || self.status == OrderStatus::Confirmed
    }

    /// Returns the number of products in the order.
    pub fn item_count(&self) -> usize {
        self.products.len()
    }
}

/// Data-access interface.
pub trait Repository {
    fn find_user(&self, id: u64) -> Option<User>;
    fn find_product(&self, id: u64) -> Option<Product>;
    fn save_order(&mut self, order: &Order) -> Result<(), String>;
    fn list_orders(&self, user_id: u64) -> Vec<Order>;
}

/// Logging interface.
pub trait Logger {
    fn info(&self, msg: &str);
    fn warn(&self, msg: &str);
    fn error(&self, msg: &str);
}

/// Notification interface.
pub trait Notifier {
    fn notify_user(&self, user_id: u64, message: &str) -> Result<(), String>;
}

/// In-memory repository for testing.
pub struct InMemoryRepository {
    users: HashMap<u64, User>,
    products: HashMap<u64, Product>,
    orders: Vec<Order>,
}

impl InMemoryRepository {
    pub fn new() -> Self {
        InMemoryRepository {
            users: HashMap::new(),
            products: HashMap::new(),
            orders: Vec::new(),
        }
    }
}

impl Repository for InMemoryRepository {
    fn find_user(&self, id: u64) -> Option<User> {
        self.users.get(&id).cloned()
    }

    fn find_product(&self, id: u64) -> Option<Product> {
        self.products.get(&id).cloned()
    }

    fn save_order(&mut self, order: &Order) -> Result<(), String> {
        self.orders.push(order.clone());
        Ok(())
    }

    fn list_orders(&self, user_id: u64) -> Vec<Order> {
        self.orders
            .iter()
            .filter(|o| o.user_id == user_id)
            .cloned()
            .collect()
    }
}

impl fmt::Display for User {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "User({}, {})", self.id, self.name)
    }
}
