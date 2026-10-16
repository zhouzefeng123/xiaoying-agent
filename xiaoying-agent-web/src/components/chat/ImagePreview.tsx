import React, { useState } from 'react';
import { Download, Maximize2, X, FolderOpen } from 'lucide-react';
import { toast } from 'sonner';

interface ImagePreviewProps {
    src: string;
    alt?: string;
}

export const ImagePreview: React.FC<ImagePreviewProps> = ({ src, alt }) => {
    const [isOpen, setIsOpen] = useState(false);

    const handleDownload = async (e: React.MouseEvent) => {
        e.stopPropagation();
        try {
            const response = await fetch(src);
            const blob = await response.blob();
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            // Try to get filename from URL or use alt or default
            const urlFilename = src.split('/').pop()?.split('?')[0];
            a.download = urlFilename || alt || 'image.png';
            document.body.appendChild(a);
            a.click();
            window.URL.revokeObjectURL(url);
            document.body.removeChild(a);
        } catch (error) {
            console.error('Download failed:', error);
            window.open(src, '_blank');
        }
    };

    const handleOpenFolder = async (e: React.MouseEvent) => {
        e.stopPropagation();
        try {
            // Extract relative path from src if it's a workspace path
            // http://localhost:3000/workspace/images/xxx.png -> workspace/images/xxx.png
            let path = src;
            if (src.includes('/workspace/')) {
                path = 'workspace/' + src.split('/workspace/')[1];
            } else {
                toast.error("只能打开工作区文件的位置");
                return;
            }

            const res = await fetch('/v1/system/open', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ path })
            });
            
            if (!res.ok) {
                throw new Error('Failed to open folder');
            }
            toast.success("已打开所在文件夹");
        } catch (error) {
            console.error('Failed to open folder:', error);
            toast.error("无法打开文件夹");
        }
    };

    return (
        <>
            <div className="group relative inline-block my-2 rounded-lg overflow-hidden border border-zinc-200 dark:border-zinc-700 bg-zinc-100 dark:bg-zinc-800">
                <img 
                    src={src} 
                    alt={alt || "Image"} 
                    className="max-w-full h-auto max-h-[300px] object-contain cursor-zoom-in"
                    onClick={() => setIsOpen(true)}
                    loading="lazy"
                />
                
                <div className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity flex gap-2">
                    <button 
                        onClick={handleOpenFolder}
                        className="p-1.5 bg-black/50 hover:bg-black/70 text-white rounded-md backdrop-blur-sm transition-colors"
                        title="打开所在文件夹"
                    >
                        <FolderOpen className="w-4 h-4" />
                    </button>
                    <button 
                        onClick={handleDownload}
                        className="p-1.5 bg-black/50 hover:bg-black/70 text-white rounded-md backdrop-blur-sm transition-colors"
                        title="下载图片"
                    >
                        <Download className="w-4 h-4" />
                    </button>
                    <button 
                        onClick={() => setIsOpen(true)}
                        className="p-1.5 bg-black/50 hover:bg-black/70 text-white rounded-md backdrop-blur-sm transition-colors"
                        title="查看大图"
                    >
                        <Maximize2 className="w-4 h-4" />
                    </button>
                </div>
            </div>

            {isOpen && (
                <div 
                    className="fixed inset-0 z-[100] flex items-center justify-center bg-black/90 backdrop-blur-sm animate-in fade-in duration-200"
                    onClick={() => setIsOpen(false)}
                >
                    <div className="relative max-w-[95vw] max-h-[95vh] flex flex-col items-center">
                        <button 
                            onClick={() => setIsOpen(false)}
                            className="absolute -top-12 right-0 p-2 text-white/70 hover:text-white hover:bg-white/10 rounded-full transition-colors"
                        >
                            <X className="w-6 h-6" />
                        </button>
                        
                        <img 
                            src={src} 
                            alt={alt || "Full Preview"} 
                            className="max-w-full max-h-[85vh] object-contain rounded-md shadow-2xl cursor-zoom-out"
                            onClick={() => setIsOpen(false)}
                        />
                        
                         <div className="mt-4 flex gap-4">
                            <button 
                                onClick={handleOpenFolder}
                                className="flex items-center gap-2 px-4 py-2 bg-white/10 hover:bg-white/20 text-white rounded-full backdrop-blur-md transition-colors font-medium text-sm"
                            >
                                <FolderOpen className="w-4 h-4" />
                                打开文件夹
                            </button>
                            <button 
                                onClick={handleDownload}
                                className="flex items-center gap-2 px-4 py-2 bg-white/10 hover:bg-white/20 text-white rounded-full backdrop-blur-md transition-colors font-medium text-sm"
                            >
                                <Download className="w-4 h-4" />
                                下载原图
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </>
    );
};
