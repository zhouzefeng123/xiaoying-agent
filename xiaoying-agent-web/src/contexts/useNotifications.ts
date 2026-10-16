import { useContext } from 'react';
import { NotificationContext, type Notification } from './notification-context';

const fallbackContext = {
    notifications: [] as Notification[],
    unreadCount: 0,
    addNotification: () => {},
    markAsRead: () => {},
    markAllAsRead: () => {},
    clearAll: () => {},
    removeNotification: () => {}
};

export function useNotifications() {
    const context = useContext(NotificationContext);
    return context ?? fallbackContext;
}
