import { useState, useCallback, useEffect, type ReactNode } from 'react';
import { NotificationContext, type Notification } from './notification-context';

const STORAGE_KEY = 'auramate_notifications';

function toSafeText(value: unknown): string {
    if (typeof value === 'string') {
        return value;
    }
    if (value == null) {
        return '';
    }
    if (typeof value === 'object') {
        try {
            return JSON.stringify(value);
        } catch {
            return String(value);
        }
    }
    return String(value);
}

function normalizeTimestamp(value: unknown): number {
    if (typeof value === 'number' && Number.isFinite(value)) {
        return value;
    }
    if (typeof value === 'string') {
        const num = Number(value);
        if (Number.isFinite(num)) {
            return num;
        }
        const parsed = Date.parse(value);
        if (!Number.isNaN(parsed)) {
            return parsed;
        }
    }
    return Date.now();
}

function normalizeNotifications(value: unknown): Notification[] {
    if (!Array.isArray(value)) {
        return [];
    }
    return value.map((item, index) => {
        const record = item && typeof item === 'object' ? item as Record<string, unknown> : {};
        const id = toSafeText(record.id).trim() || `${Date.now()}_${index}`;
        const title = toSafeText(record.title).trim() || '通知';
        const content = toSafeText(record.content);
        const timestamp = normalizeTimestamp(record.timestamp);
        const read = Boolean(record.read);
        return { id, title, content, timestamp, read };
    });
}

export function NotificationProvider({ children }: { children: ReactNode }) {
    const [notifications, setNotifications] = useState<Notification[]>(() => {
        try {
            const stored = localStorage.getItem(STORAGE_KEY);
            return stored ? normalizeNotifications(JSON.parse(stored)) : [];
        } catch (error) {
            console.error('Failed to load notifications from storage:', error);
            return [];
        }
    });

    // Persist to local storage whenever notifications change
    useEffect(() => {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(notifications));
        } catch (error) {
            console.error('Failed to save notifications to storage:', error);
        }
    }, [notifications]);

    const addNotification = useCallback((title: string, content: string) => {
        const newNotification: Notification = {
            id: Date.now().toString() + Math.random().toString(36).substr(2, 9),
            title: toSafeText(title).trim() || '通知',
            content: toSafeText(content),
            timestamp: Date.now(),
            read: false,
        };
        setNotifications(prev => [newNotification, ...prev]);
    }, []);

    const markAsRead = useCallback((id: string) => {
        setNotifications(prev => prev.map(n => 
            n.id === id ? { ...n, read: true } : n
        ));
    }, []);

    const markAllAsRead = useCallback(() => {
        setNotifications(prev => prev.map(n => ({ ...n, read: true })));
    }, []);

    const clearAll = useCallback(() => {
        setNotifications([]);
    }, []);

    const removeNotification = useCallback((id: string) => {
        setNotifications(prev => prev.filter(n => n.id !== id));
    }, []);

    const unreadCount = notifications.filter(n => !n.read).length;

    return (
        <NotificationContext.Provider value={{
            notifications,
            unreadCount,
            addNotification,
            markAsRead,
            markAllAsRead,
            clearAll,
            removeNotification
        }}>
            {children}
        </NotificationContext.Provider>
    );
}
