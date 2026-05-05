// models.zig — Zig sample for code-graph test fixtures.
// Provides named struct types, enum types, struct methods, and @import calls.
// Must produce at least 6 symbols.

const std = @import("std");
const mem = @import("mem");

/// Status of a user account.
pub const UserStatus = enum {
    Active,
    Inactive,
    Banned,
};

/// A user in the system.
pub const User = struct {
    id: u64 = 0,
    name: []const u8 = "",
    email: []const u8 = "",
    status: UserStatus = UserStatus.Active,

    /// Create a new active user.
    pub fn init(id: u64, name: []const u8, email: []const u8) User {
        return User{ .id = id, .name = name, .email = email };
    }

    /// Returns true if the user account is active.
    pub fn isActive(self: User) bool {
        return self.status == UserStatus.Active;
    }
};

/// A product in the catalog.
pub const Product = struct {
    id: u64 = 0,
    name: []const u8 = "",
    price: f64 = 0.0,
    stock: u32 = 0,

    /// Returns true if the product is in stock.
    pub fn isAvailable(self: Product) bool {
        return self.stock > 0;
    }

    /// Returns the discounted price.
    pub fn discountedPrice(self: Product, pct: f64) f64 {
        return self.price * (1.0 - pct / 100.0);
    }
};

/// Status of an order.
pub const OrderStatus = enum {
    Pending,
    Confirmed,
    Shipped,
    Delivered,
    Cancelled,
};

/// A purchase order.
pub const Order = struct {
    id: u64 = 0,
    user_id: u64 = 0,
    total: f64 = 0.0,
    status: OrderStatus = OrderStatus.Pending,

    /// Returns true if the order can still be cancelled.
    pub fn isCancellable(self: Order) bool {
        return self.status == OrderStatus.Pending or self.status == OrderStatus.Confirmed;
    }
};
