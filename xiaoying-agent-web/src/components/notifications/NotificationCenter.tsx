import { useState, useRef, useEffect, type CSSProperties } from 'react';
import { Bell, Check, Trash2 } from 'lucide-react';
import { useNotifications } from '../../contexts/useNotifications';
import { cn } from '../../lib/utils';

type Notification = {
    id: string;
    title: string;
    content: string;
    timestamp: number;
    read: boolean;
};

function NotificationItem({ notification, onMarkAsRead, onDelete, onViewInChat }: { 
    notification: Notification, 
    onMarkAsRead: (id: string, e: React.MouseEvent<Element>) => void,
    onDelete: (id: string, e: React.MouseEvent<Element>) => void,
    onViewInChat?: (notification: Notification) => void
}) {
    const handleClick = (e: React.MouseEvent) => {
        onViewInChat?.(notification);
        if (!notification.read) {
            onMarkAsRead(notification.id, e);
        }
    };

    return (
        <div
            onClick={handleClick}
            className={cn(
                "p-3 rounded-lg text-left transition-colors relative group cursor-pointer hover:bg-zinc-50 dark:hover:bg-zinc-800/50",
                notification.read
                    ? "bg-transparent text-zinc-500"
                    : "bg-blue-50/50 dark:bg-blue-900/10 text-zinc-900 dark:text-zinc-100"
            )}
        >
            {!notification.read && (
                <div className="absolute top-3 left-2 w-1.5 h-1.5 bg-blue-500 rounded-full" />
            )}
            <div className={cn("pl-2", !notification.read && "pl-3")}>
                <div className="flex items-start justify-between gap-2 mb-1">
                    <span className="text-xs font-medium leading-none">
                        {notification.title}
                    </span>
                    <span className="text-[10px] text-zinc-400 whitespace-nowrap">
                        {new Date(notification.timestamp).toLocaleTimeString('zh-CN', {
                            hour: '2-digit',
                            minute: '2-digit'
                        })}
                    </span>
                </div>
                <p className="text-xs text-zinc-500 dark:text-zinc-400 leading-relaxed line-clamp-3">
                    {notification.content}
                </p>
                
                <div className="absolute top-2 right-2 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                    {!notification.read && (
                        <button
                            onClick={(e) => {
                                e.stopPropagation();
                                onMarkAsRead(notification.id, e);
                            }}
                            className="p-1 text-zinc-400 hover:text-blue-500 hover:bg-blue-100 dark:hover:bg-blue-900/30 rounded transition-all"
                            title="标为已读"
                        >
                            <Check className="w-3 h-3" />
                        </button>
                    )}
                    <button
                        onClick={(e) => {
                            e.stopPropagation();
                            onDelete(notification.id, e);
                        }}
                        className="p-1 text-zinc-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 rounded transition-all"
                        title="删除消息"
                    >
                        <Trash2 className="w-3 h-3" />
                    </button>
                </div>
            </div>
        </div>
    );
}

interface NotificationCenterProps {
    onNotificationClick?: (notification: Notification) => void;
    onOpen?: () => void;
}

export function NotificationCenter({ onNotificationClick, onOpen }: NotificationCenterProps) {
    const [isOpen, setIsOpen] = useState(false);
    const { notifications, unreadCount, markAsRead, markAllAsRead, clearAll, removeNotification } = useNotifications();
    const containerRef = useRef<HTMLDivElement>(null);

    // Close on click outside
    useEffect(() => {
        function handleClickOutside(event: MouseEvent) {
            if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
                setIsOpen(false);
            }
        }
        if (isOpen) {
            document.addEventListener('mousedown', handleClickOutside);
        }
        return () => {
            document.removeEventListener('mousedown', handleClickOutside);
        };
    }, [isOpen]);

    const handleMarkAsRead = (id: string, e: React.MouseEvent) => {
        e.stopPropagation();
        markAsRead(id);
    };

    const handleDelete = (id: string, e: React.MouseEvent) => {
        e.stopPropagation();
        removeNotification(id);
    };

    const noDragRegionStyle: CSSProperties & { WebkitAppRegion?: 'no-drag' } = { WebkitAppRegion: 'no-drag' };

    return (
        <div className="relative" ref={containerRef} style={noDragRegionStyle}>
            <button
                onClick={() => {
                    setIsOpen(!isOpen);
                    onOpen?.();
                }}
                className="relative p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                title="消息通知"
            >
                <Bell className="w-4 h-4" />
                {unreadCount > 0 && (
                    <span className="absolute top-1 right-1 w-2 h-2 bg-red-500 rounded-full ring-2 ring-white dark:ring-zinc-900" />
                )}
            </button>

            {isOpen && (
                <div className="absolute right-0 top-full mt-2 w-80 bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl shadow-lg z-50 overflow-hidden flex flex-col max-h-[500px]">
                    <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-100 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-900/50">
                        <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100">消息通知</h3>
                        <div className="flex items-center gap-1">
                            {notifications.length > 0 && (
                                <>
                                    <button
                                        onClick={markAllAsRead}
                                        className="p-1 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300 rounded hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
                                        title="全部已读"
                                    >
                                        <Check className="w-3.5 h-3.5" />
                                    </button>
                                    <button
                                        onClick={clearAll}
                                        className="p-1 text-zinc-400 hover:text-red-600 dark:hover:text-red-400 rounded hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
                                        title="清空消息"
                                    >
                                        <Trash2 className="w-3.5 h-3.5" />
                                    </button>
                                </>
                            )}
                        </div>
                    </div>

                    <div className="overflow-y-auto flex-1 p-2 space-y-2">
                        {notifications.length === 0 ? (
                            <div className="flex flex-col items-center justify-center py-8 text-zinc-400">
                                <Bell className="w-8 h-8 mb-2 opacity-20" />
                                <span className="text-xs">暂无消息</span>
                            </div>
                        ) : (
                            notifications.map(notification => (
                                <NotificationItem 
                                    key={notification.id} 
                                    notification={notification} 
                                    onMarkAsRead={handleMarkAsRead}
                                    onDelete={handleDelete}
                                    onViewInChat={onNotificationClick}
                                />
                            ))
                        )}
                    </div>
                </div>
            )}
        </div>
    );
}
