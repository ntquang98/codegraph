// service.ts — order service for the TypeScript sample
import {
    User, Product, Order, OrderStatus,
    Repository, Logger, Notifier,
    ValidationError, NotFoundError,
    isUserActive, isOrderCancellable, formatOrderSummary,
} from "./models";

export class OrderService {
    constructor(
        private readonly repo: Repository,
        private readonly logger: Logger,
        private readonly notifier: Notifier,
    ) { }

    async placeOrder(userId: number, productIds: number[]): Promise<Order> {
        const user = await this.findUser(userId);
        if (!isUserActive(user)) {
            throw new ValidationError("userId", `User ${userId} is not active`);
        }

        const products = await this.fetchProducts(productIds);
        this.checkStock(products);

        const order = this.buildOrder(user, products);
        validateOrder(order);

        await this.repo.saveOrder(order);
        this.logger.info("order placed successfully");

        await this.notifier.notifyUser(userId, "Your order has been placed").catch(() => {
            this.logger.warn("failed to notify user");
        });

        return order;
    }

    async cancelOrder(orderId: number): Promise<void> {
        const orders = await this.repo.listOrders(0);
        const target = orders.find(o => o.id === orderId);

        if (!target) {
            throw new NotFoundError("Order", orderId);
        }

        if (!isOrderCancellable(target)) {
            throw new ValidationError("status", `Order ${orderId} cannot be cancelled`);
        }

        await this.repo.updateOrderStatus(orderId, OrderStatus.Cancelled);
        this.logger.info("order cancelled");

        await this.notifier.notifyUser(target.userId, "Your order has been cancelled").catch(() => {
            this.logger.warn("failed to notify user on cancel");
        });
    }

    async getOrderSummary(orderId: number): Promise<string> {
        const orders = await this.repo.listOrders(0);
        const order = orders.find(o => o.id === orderId);
        if (!order) {
            throw new NotFoundError("Order", orderId);
        }
        return formatOrderSummary(order);
    }

    private async findUser(userId: number): Promise<User> {
        const user = await this.repo.findUser(userId);
        if (!user) {
            throw new NotFoundError("User", userId);
        }
        return user;
    }

    private async fetchProducts(ids: number[]): Promise<Product[]> {
        const products: Product[] = [];
        for (const id of ids) {
            const product = await this.repo.findProduct(id);
            if (!product) {
                throw new NotFoundError("Product", id);
            }
            products.push(product);
        }
        return products;
    }

    private checkStock(products: Product[]): void {
        for (const p of products) {
            if (p.stock <= 0) {
                throw new ValidationError("stock", `Product ${p.id} is out of stock`);
            }
        }
    }

    private buildOrder(user: User, products: Product[]): Order {
        const total = calculateTotal(products);
        return {
            id: 0,
            userId: user.id,
            products,
            total,
            status: OrderStatus.Pending,
            createdAt: new Date(),
        };
    }
}

export function calculateTotal(products: Product[]): number {
    return products.reduce((sum, p) => sum + p.price, 0);
}

export function validateOrder(order: Order): void {
    if (!order) {
        throw new ValidationError("order", "Order is null");
    }
    if (order.userId <= 0) {
        throw new ValidationError("userId", `Invalid user ID: ${order.userId}`);
    }
    if (order.products.length === 0) {
        throw new ValidationError("products", "Order has no products");
    }
    if (order.total < 0) {
        throw new ValidationError("total", "Order total cannot be negative");
    }
}
