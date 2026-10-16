import React, { useState, type ReactNode, useEffect, useCallback, useRef } from 'react';
import { AuthContext, type AuthContextType, type UserProfile } from './auth-context';
import { toast } from 'sonner';

const USER_REFRESH_INTERVAL_MS = 60_000;

function readStoredUser(): UserProfile | null {
    const storedUser = localStorage.getItem('auth_user');
    if (!storedUser) {
        return null;
    }

    try {
        return JSON.parse(storedUser) as UserProfile;
    } catch (error) {
        console.error('Failed to parse stored user', error);
        localStorage.removeItem('auth_user');
        return null;
    }
}

function normalizeUserProfile(rawUser: any, fallbackUsername = ''): UserProfile | null {
    if (!rawUser || typeof rawUser !== 'object') {
        return null;
    }

    const rawMember = rawUser.member && typeof rawUser.member === 'object' ? rawUser.member : null;

    return {
        id: rawUser.id || rawUser.userId || 'user',
        username: rawUser.username || rawUser.name || rawUser.nickname || fallbackUsername || 'user',
        nickname: rawUser.nickname || rawUser.username || rawUser.name || fallbackUsername || undefined,
        email: rawUser.email || fallbackUsername || undefined,
        memberUsable: typeof rawUser.member_usable === 'boolean' ? rawUser.member_usable : undefined,
        member: rawMember ? {
            status: typeof rawMember.status === 'string' ? rawMember.status : undefined,
            expireAt: typeof rawMember.expire_at === 'number' ? rawMember.expire_at : undefined,
            actualPaid: typeof rawMember.actual_paid === 'boolean' ? rawMember.actual_paid : undefined,
        } : undefined,
    };
}

export const AuthProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
    const [user, setUser] = useState<UserProfile | null>(null);
    const [isLoading, setIsLoading] = useState(true);
    const userRef = useRef<UserProfile | null>(null);
    const authStateVersionRef = useRef(0);
    const activeRefreshControllerRef = useRef<AbortController | null>(null);

    const invalidateAuthState = useCallback(() => {
        authStateVersionRef.current += 1;
        activeRefreshControllerRef.current?.abort();
        activeRefreshControllerRef.current = null;
    }, []);

    const clearAuthState = useCallback(() => {
        localStorage.removeItem('auth_token');
        localStorage.removeItem('auth_user');
        invalidateAuthState();
        setUser(null);
    }, [invalidateAuthState]);

    useEffect(() => {
        userRef.current = user;
    }, [user]);

    useEffect(() => {
        const token = localStorage.getItem('auth_token');
        if (token) {
            const storedUser = readStoredUser();
            if (storedUser) {
                setUser(storedUser);
            } else {
                clearAuthState();
            }
        }
        setIsLoading(false);
    }, [clearAuthState]);

    const refreshUser: AuthContextType['refreshUser'] = useCallback(async () => {
        const token = localStorage.getItem('auth_token');
        if (!token) {
            return;
        }

        const requestVersion = authStateVersionRef.current;
        const controller = new AbortController();

        activeRefreshControllerRef.current?.abort();
        activeRefreshControllerRef.current = controller;
        
        try {
            const res = await fetch('/v1/user/current', {
                headers: { 'Authorization': `Bearer ${token}` },
                signal: controller.signal,
            });

            if (controller.signal.aborted || requestVersion !== authStateVersionRef.current || localStorage.getItem('auth_token') !== token) {
                return;
            }

            if (res.status === 401) {
                clearAuthState();
                return;
            }

            if (res.ok) {
                const data = await res.json();
                if (controller.signal.aborted || requestVersion !== authStateVersionRef.current || localStorage.getItem('auth_token') !== token) {
                    return;
                }

                const fallbackUsername = userRef.current?.username || readStoredUser()?.username || '';
                const userData = normalizeUserProfile(data?.data ?? data, fallbackUsername);
                if (userData) {
                    setUser(userData);
                    localStorage.setItem('auth_user', JSON.stringify(userData));
                }
            }
        } catch (e) {
            if (e instanceof Error && e.name === 'AbortError') {
                return;
            }
            console.error('Failed to refresh user', e);
        } finally {
            if (activeRefreshControllerRef.current === controller) {
                activeRefreshControllerRef.current = null;
            }
        }
    }, [clearAuthState]);

    useEffect(() => {
        if (!localStorage.getItem('auth_token')) {
            return;
        }

        void refreshUser();

        const handleWindowFocus = () => {
            void refreshUser();
        };

        const handleVisibilityChange = () => {
            if (document.visibilityState === 'visible') {
                void refreshUser();
            }
        };

        const intervalId = window.setInterval(() => {
            if (document.visibilityState === 'visible') {
                void refreshUser();
            }
        }, USER_REFRESH_INTERVAL_MS);

        window.addEventListener('focus', handleWindowFocus);
        document.addEventListener('visibilitychange', handleVisibilityChange);

        return () => {
            window.clearInterval(intervalId);
            window.removeEventListener('focus', handleWindowFocus);
            document.removeEventListener('visibilitychange', handleVisibilityChange);
        };
    }, [refreshUser]);

    const login: AuthContextType['login'] = useCallback(async (username, code) => {
        try {
            console.log('Attempting login:', { username, code });
            const res = await fetch('/v1/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, code }) // Use 'username' instead of 'email'
            });
            
            const data = await res.json();
            console.log('Login response:', data);
            
            if (!res.ok) {
                throw new Error(data.message || 'Login failed');
            }

            // Check for success status or code
            // Accept code 0, 200, or status 'success'/'ok'
            // Also handle if token is directly in data or in data.data
            const isSuccess = data.code === 0 || data.code === 200 || 
                              data.status === 'success' || data.status === 'ok' ||
                              (data.data && (data.data.code === 0 || data.data.code === 200));

            if (isSuccess) {
                 // Extract token and user data
                 // Could be in data.data or data root
                 const responseData = data.data || data;
                 const token = responseData.token || data.token;
                 
                 // Map response user to UserProfile
                 const rawUser = responseData.user || responseData;
                 const userInfo = normalizeUserProfile(rawUser, username) || {
                     id: 'user',
                     username,
                     nickname: username,
                     email: username
                 };
                 
                 if (token) {
                     invalidateAuthState();
                     localStorage.setItem('auth_token', token);
                     localStorage.setItem('auth_user', JSON.stringify(userInfo));
                     setUser(userInfo);
                     await refreshUser();
                     toast.success('登录成功');
                     
                     // Preserve user-managed persisted window size after login.
                 } else {
                     console.error('Login successful but no token:', data);
                     throw new Error('Login successful but no token received');
                 }
            } else {
                throw new Error(data.msg || data.message || data.error || 'Login failed');
            }
        } catch (error: any) {
            console.error("Login error", error);
            toast.error(error.message || '登录失败，请重试');
            throw error;
        }
    }, [invalidateAuthState, refreshUser]);

    const sendCode: AuthContextType['sendCode'] = useCallback(async (email, captchaVerifyParam) => {
        try {
            const res = await fetch('/v1/auth/send-email-code', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ email, captchaVerifyParam })
            });
            
            const data = await res.json();
            console.log('Send code response:', data); // Log full response
            
            // Allow code 0, 200, or if data.status is 'success'
            // Also check if data itself is "success" string or if it has send_at (from OpenAPI spec)
            if (res.ok) {
                // If the response is a simple string "success" or similar
                if (typeof data === 'string' && data.toLowerCase().includes('success')) {
                    toast.success('验证码已发送');
                    return;
                }
                
                // Check structured response
                if (data.code === 0 || data.code === 200 || data.status === 'success' || data.send_at) {
                    toast.success('验证码已发送');
                    return;
                }
                
                // Check if data is nested in data.data
                if (data.data && (data.data.send_at || data.data.status === 'success' || data.status === 'ok')) {
                    toast.success('验证码已发送');
                    return;
                }
            }

            throw new Error(data.msg || data.message || data.error || JSON.stringify(data) || '发送失败');
        } catch (error: any) {
            console.error("Send code error", error);
            // Don't show toast here if we want to let caller handle it, 
            // but the current contract seems to be void return.
            // Let's keep showing toast but maybe log more details.
            // toast.error(error.message || '验证码发送失败'); // Let component handle error toast
            throw error;
        }
    }, []);

    const logout: AuthContextType['logout'] = useCallback(() => {
        clearAuthState();
    }, [clearAuthState]);

    return (
        <AuthContext.Provider value={{ user, isLoading, login, logout, refreshUser, sendCode }}>
            {children}
        </AuthContext.Provider>
    );
};
