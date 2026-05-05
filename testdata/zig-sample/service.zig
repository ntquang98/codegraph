// service.zig — Zig sample for code-graph test fixtures.
// Provides top-level functions, function calls, and @import calls.
// Must produce at least 5 symbols and 5 edges.

const std = @import("std");
const models = @import("models.zig");

/// Check that all products are in stock.
pub fn checkStock(products: []const models.Product) bool {
    for (products) |p| {
        if (!p.isAvailable()) {
            return false;
        }
    }
    return true;
}

/// Calculate the total price of a list of products.
pub fn calculateTotal(products: []const models.Product) f64 {
    var total: f64 = 0.0;
    for (products) |p| {
        total += p.price;
    }
    return total;
}

/// Validate an order before processing.
pub fn validateOrder(user_id: u64, product_count: usize, total: f64) bool {
    if (user_id == 0) return false;
    if (product_count == 0) return false;
    if (total < 0.0) return false;
    return true;
}

/// Place an order: validate, check stock, and return total.
pub fn placeOrder(user_id: u64, products: []const models.Product) f64 {
    if (!validateOrder(user_id, products.len, calculateTotal(products))) {
        return -1.0;
    }
    if (!checkStock(products)) {
        return -1.0;
    }
    return calculateTotal(products);
}

/// Format a simple order summary string.
pub fn formatSummary(order_id: u64, total: f64) void {
    _ = order_id;
    _ = total;
    const writer = std.io.getStdOut().writer();
    _ = writer;
}
