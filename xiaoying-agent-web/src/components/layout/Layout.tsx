import React, { type ReactNode } from 'react';

interface LayoutProps {
    children: ReactNode;
}

export const Layout: React.FC<LayoutProps> = ({ children }) => {
    return (
        <div className="relative w-full h-screen overflow-hidden bg-transparent">
            {/* Draggable Title Bar Area */}
            <div 
                className="absolute top-0 left-0 w-full h-8 z-[100]" 
                style={{ WebkitAppRegion: 'drag' } as React.CSSProperties}
            >
                {/* 
                   We keep this empty and transparent. 
                   It just provides a handle to drag the window.
                   pointer-events-none is tricky: if set, does drag work?
                   Usually 'drag' region consumes mouse events so clicks don't pass through to underlying elements
                   unless those elements are marked as 'no-drag'.
                   
                   However, if we set pointer-events-none, CSS drag might not work?
                   Actually, -webkit-app-region: drag makes the element behave like the window title bar.
                   It usually captures the events.
                   
                   If we have interactive elements at the top (like close buttons), they need -webkit-app-region: no-drag.
                */}
            </div>
            
            {/* Main Content */}
            <div className="w-full h-full">
                {children}
            </div>
        </div>
    );
};
