import { createContext } from 'react';

export interface UserMemberProfile {
    status?: string;
    expireAt?: number;
    actualPaid?: boolean;
}

export interface UserProfile {
    id: string;
    username: string;
    nickname?: string;
    email?: string;
    memberUsable?: boolean;
    member?: UserMemberProfile;
}

export interface AuthContextType {
    user: UserProfile | null;
    isLoading: boolean;
    login: (username: string, code: string) => Promise<void>;
    logout: () => void;
    refreshUser: () => Promise<void>;
    sendCode: (email: string) => Promise<void>;
}

export const AuthContext = createContext<AuthContextType | undefined>(undefined);
