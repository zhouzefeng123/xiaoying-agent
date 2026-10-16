import { createContext } from 'react';

export interface Notification {
    id: string;
    title: string;
    content: string;
    timestamp: number;
    read: boolean;
}

export interface NotificationContextType {
    notifications: Notification[];
    unreadCount: number;
    addNotification: (title: string, content: string) => void;
    markAsRead: (id: string) => void;
    markAllAsRead: () => void;
    clearAll: () => void;
    removeNotification: (id: string) => void;
}

export const NotificationContext = createContext<NotificationContextType | undefined>(undefined);
