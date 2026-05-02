# models.py — domain types for the Python sample
from dataclasses import dataclass, field
from typing import List, Optional
from enum import Enum


class UserStatus(Enum):
    ACTIVE = "active"
    INACTIVE = "inactive"
    BANNED = "banned"


class OrderStatus(Enum):
    PENDING = "pending"
    CONFIRMED = "confirmed"
    SHIPPED = "shipped"
    DELIVERED = "delivered"
    CANCELLED = "cancelled"


@dataclass
class User:
    id: int
    name: str
    email: str
    status: UserStatus = UserStatus.ACTIVE

    def is_active(self) -> bool:
        """Return True if the user account is active."""
        return self.status == UserStatus.ACTIVE

    def display_name(self) -> str:
        """Return a formatted display name."""
        return f"{self.name} <{self.email}>"


@dataclass
class Product:
    id: int
    name: str
    price: float
    stock: int = 0
    category: str = ""

    def is_available(self) -> bool:
        """Return True if the product is in stock."""
        return self.stock > 0

    def discounted_price(self, pct: float) -> float:
        """Return the price after applying a discount percentage."""
        return self.price * (1 - pct / 100)


@dataclass
class Order:
    user_id: int
    products: List[Product] = field(default_factory=list)
    total: float = 0.0
    id: int = 0
    status: OrderStatus = OrderStatus.PENDING

    def is_cancellable(self) -> bool:
        """Return True if the order can still be cancelled."""
        return self.status in (OrderStatus.PENDING, OrderStatus.CONFIRMED)

    def item_count(self) -> int:
        """Return the number of products in the order."""
        return len(self.products)


class Repository:
    """Abstract base for data access."""

    def find_user(self, user_id: int) -> Optional[User]:
        raise NotImplementedError

    def find_product(self, product_id: int) -> Optional[Product]:
        raise NotImplementedError

    def save_order(self, order: Order) -> None:
        raise NotImplementedError

    def list_orders(self, user_id: int) -> List[Order]:
        raise NotImplementedError

    def update_order_status(self, order_id: int, status: OrderStatus) -> None:
        raise NotImplementedError


class Logger:
    """Abstract base for logging."""

    def info(self, msg: str) -> None:
        raise NotImplementedError

    def warn(self, msg: str) -> None:
        raise NotImplementedError

    def error(self, msg: str, err: Exception) -> None:
        raise NotImplementedError


def format_order_summary(order: Order) -> str:
    """Return a human-readable summary of an order."""
    return (
        f"Order #{order.id}: {order.item_count()} items, "
        f"total ${order.total:.2f}, status: {order.status.value}"
    )


def calculate_total(products: List[Product]) -> float:
    """Sum the prices of all products."""
    return sum(p.price for p in products)
