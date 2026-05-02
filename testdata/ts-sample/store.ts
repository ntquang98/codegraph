// store.ts — in-memory repository and utilities for the TypeScript sample
import { User, Product, Order, OrderStatus, Repository, Logger, Notifier } from "./models";

export class InMemoryRepository implements Repository {
    private users = new Map<number, User>();
    private products = new Map<number, Product>();
    private orders = new Map<number, Order>();
    private nextId = 1;

    async findUser(id: number): Promise<User | null> {
        return this.users.get(id) ?? null;
    }

    async findProduct(id: number): Promise<Product | null> {
        return this.products.get(id) ?? null;
    }

    async saveOrder(order: Order): Promise<void> {
        if (!order.id) {
            order.id = this.nextId++;
        }
        this.orders.set(order.id, order);
    }

    async listOrders(userId: number): Promise<Order[]> {
        const all = Array.from(this.orders.values());
        return userId === 0 ? all : all.filter(o => o.userId === userId);
    }

    async updateOrderStatus(orderId: number, status: OrderStatus): Promise<void> {
        const order = this.orders.get(orderId);
        if (order) {
            order.status = status;
        }
    }

    addUser(user: User): void {
        this.users.set(user.id, user);
    }

    addProduct(product: Product): void {
        this.products.set(product.id, product);
    }

    countOrders(): number {
        return this.orders.size;
    }

    countUsers(): number {
        return this.users.size;
    }

    countProducts(): number {
        return this.products.size;
    }
}

export class ConsoleLogger implements Logger {
    info(msg: string): void {
        console.log("[INFO]", msg);
    }

    warn(msg: string): void {
        console.warn("[WARN]", msg);
    }

    error(msg: string, err: Error): void {
        console.error("[ERROR]", msg, err.message);
    }
}

export class NoopNotifier implements Notifier {
    async notifyUser(_userId: number, _message: string): Promise<void> {
        // no-op
    }

    async notifyAdmin(_message: string): Promise<void> {
        // no-op
    }
}

export function createTestUser(id: number, name: string): User {
    return {
        id,
        name,
        email: `${name.toLowerCase()}@example.com`,
        status: "active" as any,
        createdAt: new Date(),
    };
}

export function createTestProduct(id: number, name: string, price: number): Product {
    return { id, name, price, stock: 10, category: "test" };
}
