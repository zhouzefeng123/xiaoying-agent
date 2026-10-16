import React from 'react';
import { cn } from '../../lib/utils';
import logo from '../../assets/workbuddy-logo.svg';

interface LogoProps {
    className?: string;
}

export const Logo: React.FC<LogoProps> = ({ className }) => {
    return (
        <img 
            src={logo} 
            alt="XiaoYing" 
            className={cn("w-full h-full object-contain", className)}
        />
    );
};
