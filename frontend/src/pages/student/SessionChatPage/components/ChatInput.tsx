import React, { useRef } from 'react';
import { Button } from '../../../../components/ui/Button';
import { Send, Square, Paperclip, Image as ImageIcon, Mic, Loader2, X, FileText, AlertCircle } from 'lucide-react';
import { getDocumentAcceptTypes, formatFileSize } from '@/libs/utils/documentParser';
import type { FileUploadItem } from '../hooks/useFileUpload';

interface ChatInputProps {
  value: string;
  selectedImages: File[];
  previewUrls: string[];
  isStreaming: boolean;
  isSending: boolean;
  disabled?: boolean;
  // 文件上传相关
  files: FileUploadItem[];
  isFileParsing: boolean;
  onChange: (value: string) => void;
  onSend: () => void;
  onCancel: () => void;
  onImageSelect: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onRemoveImage: (index: number) => void;
  onFileSelect: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onRemoveFile: (index: number) => void;
}

export const ChatInput = React.memo<ChatInputProps>(
  ({
    value,
    selectedImages,
    previewUrls,
    isStreaming,
    isSending,
    disabled,
    files,
    isFileParsing,
    onChange,
    onSend,
    onCancel,
    onImageSelect,
    onRemoveImage,
    onFileSelect,
    onRemoveFile,
  }) => {
    const imageInputRef = useRef<HTMLInputElement>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const hasAttachments = previewUrls.length > 0 || files.length > 0;
    const isPreparingSend = isSending && !isStreaming;
    const isBusy = isPreparingSend || isFileParsing;
    const attachmentLocked = Boolean(
      disabled || isSending || isStreaming || isFileParsing
    );

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.nativeEvent.isComposing || e.keyCode === 229) return;
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        if (isBusy || isStreaming || disabled) return;
        onSend();
      }
    };

    const handleImageButtonClick = () => {
      imageInputRef.current?.click();
    };

    const handleFileButtonClick = () => {
      fileInputRef.current?.click();
    };

    return (
      <div className="bg-white/80 dark:bg-surface-900/80 backdrop-blur-lg border-t border-surface-200 dark:border-surface-700 p-4">
        <div className="max-w-3xl mx-auto space-y-3">
          {/* Input Box */}
          <div className="relative">
            {/* Attachments Preview Area */}
            {hasAttachments && (
              <div className="flex flex-wrap items-center gap-2 p-2 mb-2 bg-surface-50 dark:bg-surface-800 rounded-xl border border-surface-200 dark:border-surface-700">
                {/* Image Previews */}
                {previewUrls.map((url, index) => (
                  <div key={url} className="relative group">
                    <img
                      src={url}
                      alt={`预览 ${index + 1}`}
                      className="w-16 h-16 object-cover rounded-lg border border-surface-200 dark:border-surface-600"
                    />
                    <button
                      onClick={() => onRemoveImage(index)}
                      disabled={attachmentLocked}
                      className="absolute -top-1.5 -right-1.5 p-0.5 bg-red-500 text-white rounded-full opacity-0 group-hover:opacity-100 transition-opacity disabled:pointer-events-none disabled:opacity-0"
                    >
                      <X className="w-3 h-3" />
                    </button>
                  </div>
                ))}

                {/* File Previews */}
                {files.map((item, index) => (
                  <div
                    key={`${item.file.name}-${index}`}
                    className="relative group flex items-center gap-2 px-3 py-2 bg-white dark:bg-surface-700 rounded-lg border border-surface-200 dark:border-surface-600 max-w-[200px]"
                  >
                    {item.status === 'parsing' ? (
                      <Loader2 className="w-4 h-4 shrink-0 animate-spin text-primary-500" />
                    ) : item.status === 'error' ? (
                      <AlertCircle className="w-4 h-4 shrink-0 text-red-500" />
                    ) : (
                      <FileText className="w-4 h-4 shrink-0 text-primary-500" />
                    )}
                    <div className="min-w-0 flex-1">
                      <p className="text-xs font-medium text-surface-700 dark:text-surface-300 truncate">
                        {item.file.name}
                      </p>
                      <p className="text-[10px] text-surface-400">
                        {item.status === 'parsing'
                          ? '解析中...'
                          : item.status === 'error'
                            ? item.error || '解析失败'
                            : formatFileSize(item.file.size)}
                      </p>
                    </div>
                    <button
                      onClick={() => onRemoveFile(index)}
                      disabled={attachmentLocked}
                      className="absolute -top-1.5 -right-1.5 p-0.5 bg-red-500 text-white rounded-full opacity-0 group-hover:opacity-100 transition-opacity disabled:pointer-events-none disabled:opacity-0"
                    >
                      <X className="w-3 h-3" />
                    </button>
                  </div>
                ))}

                {isBusy && (
                  <div className="flex items-center space-x-2 text-sm text-surface-500">
                    <Loader2 className="w-4 h-4 animate-spin" />
                    <span>{isPreparingSend ? '上传并准备发送中...' : '解析中...'}</span>
                  </div>
                )}
              </div>
            )}

            {/* Hidden File Inputs */}
            <input
              ref={imageInputRef}
              type="file"
              accept="image/jpeg,image/png,image/gif,image/webp"
              multiple
              onChange={onImageSelect}
              disabled={attachmentLocked}
              className="hidden"
            />
            <input
              ref={fileInputRef}
              type="file"
              accept={getDocumentAcceptTypes()}
              multiple
              onChange={onFileSelect}
              disabled={attachmentLocked}
              className="hidden"
            />

            <div className="flex items-end bg-white dark:bg-surface-800 rounded-2xl shadow-sm focus-within:ring-2 focus-within:ring-primary-500/50 transition-all">
              {/* Attachment Buttons */}
              <div className="flex items-center pl-3 pb-3 space-x-1">
                <button
                  onClick={handleFileButtonClick}
                  disabled={attachmentLocked}
                  className="p-2 rounded-lg hover:bg-surface-100 dark:hover:bg-surface-700 text-surface-400 hover:text-surface-600 dark:hover:text-surface-300 transition-colors disabled:opacity-50"
                  title="上传文档 (TXT, DOCX, MD, CSV)"
                >
                  <Paperclip className="w-5 h-5" />
                </button>
                <button
                  onClick={handleImageButtonClick}
                  disabled={attachmentLocked}
                  className="p-2 rounded-lg hover:bg-surface-100 dark:hover:bg-surface-700 text-surface-400 hover:text-surface-600 dark:hover:text-surface-300 transition-colors disabled:opacity-50"
                  title="上传图片"
                >
                  <ImageIcon className="w-5 h-5" />
                </button>
              </div>

              {/* Text Input */}
              <textarea
                className="min-w-0 flex-1 bg-transparent border-none focus:ring-0 focus:outline-none resize-none py-3.5 px-2 max-h-32 min-h-14 text-surface-900 dark:text-surface-100 placeholder-surface-400 dark:placeholder-surface-500"
                placeholder="输入你的问题..."
                rows={1}
                value={value}
                onChange={(e) => onChange(e.target.value)}
                onKeyDown={handleKeyDown}
                disabled={disabled}
              />

              {/* Voice & Send/Cancel Buttons */}
              <div className="flex items-center pr-2 pb-2 space-x-1">
                <button
                  disabled={isStreaming || isSending || disabled}
                  className="p-2 rounded-lg hover:bg-surface-200 dark:hover:bg-surface-700 text-surface-400 hover:text-surface-600 dark:hover:text-surface-300 transition-colors disabled:opacity-50"
                >
                  <Mic className="w-5 h-5" />
                </button>

                {isStreaming || isSending ? (
                  <Button
                    size="icon"
                    variant="destructive"
                    className="h-10 w-10 rounded-xl"
                    onClick={onCancel}
                    aria-label="停止生成"
                    title="停止生成"
                  >
                    <Square className="w-4 h-4" />
                  </Button>
                ) : (
                  <Button
                    size="icon"
                    className="h-10 w-10 rounded-xl"
                    onClick={() => onSend()}
                    aria-label="发送"
                    title="发送"
                    disabled={
                      (!value.trim() && selectedImages.length === 0 && !files.some((f) => f.status === 'done')) ||
                      isBusy ||
                      disabled
                    }
                  >
                    <Send className="w-4 h-4" />
                  </Button>
                )}
              </div>
            </div>
          </div>

          {/* Tips */}
          <div className="flex items-center justify-center text-xs text-surface-400 dark:text-surface-500">
            <span>
              {isStreaming
                ? '生成中，可编辑但暂不可发送'
                : isSending
                  ? '正在准备发送'
                  : '按 Enter 发送，Shift + Enter 换行'}
            </span>
          </div>
        </div>
      </div>
    );
  }
);

ChatInput.displayName = 'ChatInput';
