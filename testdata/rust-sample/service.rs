// service.rs — Rust sample for code-graph test fixtures.
// Provides functions, impl blocks with trait implementations, and function calls.
// Must produce at least 8 symbols and 8 edges.

use std::fmt;

/// OrderService handles order business logic.
pub struct OrderService<R, L, N> {
    repo: R,
    logger: L,
    notifier: N,
}

impl<R, L, N> OrderService<R, L, N>
where
    R: Repository,
    L: Logger,
    N: Notifier,
{
    /// Create a new OrderService.
    pub fn new(repo: R, logger: L, notifier: N) -> Self {
        OrderService { repo, logger, notifier }
    }

    /// Place a new order for the given user and products.
    pub fn place_order(&mut self, user_id: u64, product_ids: &[u64]) -> Result<Order, String> {
        let user = self.repo.find_user(user_id).ok_or("user not found")?;
        if !user.is_active() {
            return Err(format!("user {} is not active", user_id));
        }
        let products = self.fetch_products(product_ids)?;
        check_stock(&products)?;
        let order = build_order(&user, products);
        validate_order(&order)?;
        self.repo.save_order(&order).map_err(|e| format!("save order: {}", e))?;
        self.logger.info("order placed successfully");
        let _ = self.notifier.notify_user(user_id, "Your order has been placed");
        Ok(order)
    }

    /// Cancel an existing order.
    pub fn cancel_order(&mut self, order_id: u64) -> Result<(), String> {
        let orders = self.repo.list_orders(0);
        let target = orders.into_iter().find(|o| o.id == order_id)
            .ok_or(format!("order {} not found", order_id))?;
        if !target.is_cancellable() {
            return Err(format!("order {} cannot be cancelled", order_id));
        }
        self.logger.info("order cancelled");
        let _ = self.notifier.notify_user(target.user_id, "Your order has been cancelled");
        Ok(())
    }

    /// Fetch products by their IDs.
    fn fetch_products(&self, ids: &[u64]) -> Result<Vec<Product>, String> {
        let mut products = Vec::new();
        for &id in ids {
            let p = self.repo.find_product(id).ok_or(format!("product {} not found", id))?;
            products.push(p);
        }
        Ok(products)
    }
}

/// Check that all products are in stock.
pub fn check_stock(products: &[Product]) -> Result<(), String> {
    for p in products {
        if !p.is_available() {
            return Err(format!("product {} is out of stock", p.id));
        }
    }
    Ok(())
}

/// Build an Order from a user and products.
pub fn build_order(user: &User, products: Vec<Product>) -> Order {
    let total = calculate_total(&products);
    Order {
        id: 0,
        user_id: user.id,
        products,
        total,
        status: OrderStatus::Pending,
    }
}

/// Sum the prices of all products.
pub fn calculate_total(products: &[Product]) -> f64 {
    products.iter().map(|p| p.price).sum()
}

/// Validate an order before processing.
pub fn validate_order(order: &Order) -> Result<(), String> {
    if order.user_id == 0 {
        return Err("invalid user ID".to_string());
    }
    if order.products.is_empty() {
        return Err("order has no products".to_string());
    }
    if order.total < 0.0 {
        return Err("order total cannot be negative".to_string());
    }
    Ok(())
}

/// Format a human-readable summary of an order.
pub fn format_order_summary(order: &Order) -> String {
    format!(
        "Order #{}: {} items, total ${:.2}",
        order.id,
        order.item_count(),
        order.total
    )
}

// ---- Minimal trait/type stubs so this file compiles standalone in tests ----

pub struct User {
    pub id: u64,
    pub name: String,
    pub status: UserStatus,
}

impl User {
    pub fn is_active(&self) -> bool {
        self.status == UserStatus::Active
    }
}

pub struct Product {
    pub id: u64,
    pub price: f64,
    pub stock: u32,
}

impl Product {
    pub fn is_available(&self) -> bool {
        self.stock > 0
    }
}

pub struct Order {
    pub id: u64,
    pub user_id: u64,
    pub products: Vec<Product>,
    pub total: f64,
    pub status: OrderStatus,
}

impl Order {
    pub fn is_cancellable(&self) -> bool {
        self.status == OrderStatus::Pending || self.status == OrderStatus::Confirmed
    }

    pub fn item_count(&self) -> usize {
        self.products.len()
    }
}

#[derive(PartialEq)]
pub enum UserStatus {
    Active,
    Inactive,
}

#[derive(PartialEq)]
pub enum OrderStatus {
    Pending,
    Confirmed,
    Cancelled,
}

pub trait Repository {
    fn find_user(&self, id: u64) -> Option<User>;
    fn find_product(&self, id: u64) -> Option<Product>;
    fn save_order(&mut self, order: &Order) -> Result<(), String>;
    fn list_orders(&self, user_id: u64) -> Vec<Order>;
}

pub trait Logger {
    fn info(&self, msg: &str);
}

pub trait Notifier {
    fn notify_user(&self, user_id: u64, message: &str) -> Result<(), String>;
}
