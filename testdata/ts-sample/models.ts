// models.ts — domain types for the TypeScript sample

export enum UserStatus {
    Active = "active",
    Inactive = "inactive",
    Banned = "banned",
}

export enum OrderStatus {
    Pending = "pending",
    Confirmed = "confirmed",
    Shipped = "shipped",
    Delivered = "delivered",
    Cancelled = "cancelled",
}

export interface User {
    id: number;
    name: string;
    email: string;
    status: UserStatus;
    createdAt: Date;
}

export interface Product {
    id: number;
    name: string;
    price: number;
    stock: number;
    category: string;
}

export interface Order {
    id: number;
    userId: number;
    products: Product[];
    total: number;
    status: OrderStatus;
    createdAt: Date;
}

export interface Repository {
    findUser(id: number): Promise<User | null>;
    findProduct(id: number): Promise<Product | null>;
    saveOrder(order: Order): Promise<void>;
    listOrders(userId: number): Promise<Order[]>;
    updateOrderStatus(orderId: number, status: OrderStatus): Promise<void>;
}

export interface Logger {
    info(msg: string): void;
    warn(msg: string): void;
    error(msg: string, err: Error): void;
}

export interface Notifier {
    notifyUser(userId: number, message: string): Promise<void>;
    notifyAdmin(message: string): Promise<void>;
}

export class ValidationError extends Error {
    constructor(public readonly field: string, message: string) {
        super(message);
        this.name = "ValidationError";
    }
}

export class NotFoundError extends Error {
    constructor(public readonly resource: string, public readonly id: number) {
        super(`${resource} with id ${id} not found`);
        this.name = "NotFoundError";
    }
}

export function isUserActive(user: User): boolean {
    return user.status === UserStatus.Active;
}

export function isOrderCancellable(order: Order): boolean {
    return order.status === OrderStatus.Pending || order.status === OrderStatus.Confirmed;
}

export function formatOrderSummary(order: Order): string {
    return `Order #${order.id}: ${order.products.length} items, total $${order.total.toFixed(2)}, status: ${order.status}`;
}
