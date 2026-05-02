# service.py — order service for the Python sample
from typing import List, Optional
from models import (
    User, Product, Order, OrderStatus,
    Repository, Logger,
    calculate_total, format_order_summary,
)


class OrderService:
    """Handles order business logic."""

    def __init__(self, repo: Repository, logger: Logger) -> None:
        self.repo = repo
        self.logger = logger

    def place_order(self, user_id: int, product_ids: List[int]) -> Order:
        """Create a new order for the given user and products."""
        user = self._find_user(user_id)
        if not user.is_active():
            raise ValueError(f"User {user_id} is not active")

        products = self._fetch_products(product_ids)
        self._check_stock(products)

        order = self._build_order(user, products)
        validate_order(order)

        self.repo.save_order(order)
        self.logger.info("order placed successfully")
        return order

    def cancel_order(self, order_id: int) -> None:
        """Cancel an existing order."""
        orders = self.repo.list_orders(0)
        target: Optional[Order] = None
        for o in orders:
            if o.id == order_id:
                target = o
                break

        if target is None:
            raise ValueError(f"Order {order_id} not found")

        if not target.is_cancellable():
            raise ValueError(f"Order {order_id} cannot be cancelled in status {target.status.value}")

        self.repo.update_order_status(order_id, OrderStatus.CANCELLED)
        self.logger.info("order cancelled")

    def get_order_summary(self, order_id: int) -> str:
        """Return a formatted summary of an order."""
        orders = self.repo.list_orders(0)
        for o in orders:
            if o.id == order_id:
                return format_order_summary(o)
        raise ValueError(f"Order {order_id} not found")

    def _find_user(self, user_id: int) -> User:
        user = self.repo.find_user(user_id)
        if user is None:
            raise ValueError(f"User {user_id} not found")
        return user

    def _fetch_products(self, ids: List[int]) -> List[Product]:
        products = []
        for pid in ids:
            product = self.repo.find_product(pid)
            if product is None:
                raise ValueError(f"Product {pid} not found")
            products.append(product)
        return products

    def _check_stock(self, products: List[Product]) -> None:
        for p in products:
            if not p.is_available():
                raise ValueError(f"Product {p.id} is out of stock")

    def _build_order(self, user: User, products: List[Product]) -> Order:
        total = calculate_total(products)
        return Order(user_id=user.id, products=products, total=total)


def validate_order(order: Order) -> None:
    """Validate an order before processing."""
    if order is None:
        raise ValueError("order is None")
    if order.user_id <= 0:
        raise ValueError(f"invalid user_id: {order.user_id}")
    if not order.products:
        raise ValueError("order has no products")
    if order.total < 0:
        raise ValueError("order total cannot be negative")


class InMemoryRepository(Repository):
    """In-memory implementation of Repository for testing."""

    def __init__(self) -> None:
        self._users: dict = {}
        self._products: dict = {}
        self._orders: dict = {}
        self._next_id: int = 1

    def find_user(self, user_id: int) -> Optional[User]:
        return self._users.get(user_id)

    def find_product(self, product_id: int) -> Optional[Product]:
        return self._products.get(product_id)

    def save_order(self, order: Order) -> None:
        if order.id == 0:
            order.id = self._next_id
            self._next_id += 1
        self._orders[order.id] = order

    def list_orders(self, user_id: int) -> List[Order]:
        all_orders = list(self._orders.values())
        if user_id == 0:
            return all_orders
        return [o for o in all_orders if o.user_id == user_id]

    def update_order_status(self, order_id: int, status: OrderStatus) -> None:
        if order_id in self._orders:
            self._orders[order_id].status = status

    def add_user(self, user: User) -> None:
        self._users[user.id] = user

    def add_product(self, product: Product) -> None:
        self._products[product.id] = product
