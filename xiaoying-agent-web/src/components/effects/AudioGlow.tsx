import { useEffect, useRef } from 'react';

interface AudioGlowProps {
    isRecording: boolean;
    analyser: AnalyserNode | null;
}

export function AudioGlow({ isRecording, analyser }: AudioGlowProps) {
    const overlayRef = useRef<HTMLDivElement>(null);
    const animationRef = useRef<number | null>(null);
    
    useEffect(() => {
        if (!isRecording) {
            if (overlayRef.current) {
                overlayRef.current.style.opacity = '0';
                overlayRef.current.style.boxShadow = 'none';
            }
            if (animationRef.current) {
                cancelAnimationFrame(animationRef.current);
                animationRef.current = null;
            }
            return;
        }

        // Initialize state
        const startTime = Date.now();
        
        const update = () => {
            if (!overlayRef.current) return;

            let safeVolume = 0;
            
            if (analyser) {
                // Audio Driven Mode
                const bufferLength = analyser.frequencyBinCount;
                const dataArray = new Uint8Array(bufferLength);
                analyser.getByteFrequencyData(dataArray);
                
                let sum = 0;
                const relevantLength = Math.floor(bufferLength / 2);
                for (let i = 0; i < relevantLength; i++) {
                    sum += dataArray[i];
                }
                const average = sum / relevantLength;
                
                const normalized = average / 255;
                const boosted = Math.pow(normalized, 0.5) * 1.5; 
                safeVolume = Math.min(Math.max(boosted, 0), 1.0);
            } else {
                // Breathing Mode (No audio yet)
                // Use a sine wave for smooth breathing: 0.2 to 0.8
                const now = Date.now();
                const t = (now - startTime) / 1000; // time in seconds
                // Frequency 2Hz (fast breathing), range 0.1 - 0.5
                safeVolume = 0.1 + (Math.sin(t * 4) + 1) * 0.2; 
            }

            // Render
            if (overlayRef.current) {
                overlayRef.current.style.opacity = '1';
                
                // Outer glow params - tighter to the edge
                // Reduced spread and blur to keep it "outside" and not covering content
                // Further reduced as per request: "just a small ring on the outer edge"
                const blur = 5 + safeVolume * 15; // 5px to 20px (was 20-60)
                const spread = 1 + safeVolume * 4; // 1px to 5px (was 2-10)
                const alpha = 0.6 + safeVolume * 0.4; // 0.6 to 1.0 (brighter)
                
                // Using Purple-600 (93, 33, 182)
                overlayRef.current.style.boxShadow = `inset 0 0 ${blur}px ${spread}px rgba(147, 51, 234, ${alpha})`;
            }
            
            animationRef.current = requestAnimationFrame(update);
        };

        update();

        return () => {
            if (animationRef.current) {
                cancelAnimationFrame(animationRef.current);
            }
        };
    }, [isRecording, analyser]);

    return (
        <div 
            ref={overlayRef}
            className="fixed inset-0 pointer-events-none z-[9999] transition-opacity duration-300 ease-in-out"
            style={{ opacity: 0 }}
        />
    );
}
