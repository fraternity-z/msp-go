/**
 * 数据模型类型定义
 */

import type { DifficultyLevel, UserRole } from './common';

// 用户信息
export interface User {
  id: string;
  username: string;
  email: string;
  role: UserRole;
  avatar?: string;
  createdAt: string;
  updatedAt: string;
}

// 学生信息
export interface Student extends User {
  role: 'student';
  grade?: string;
  school?: string;
}

// 教师信息
export interface Teacher extends User {
  role: 'teacher';
  department?: string;
  title?: string;
}

// 知识节点
export interface KnowledgeNode {
  id: string;
  name: string;
  description?: string;
  parentId?: string;
  level: number;
  order: number;
}

// 练习题
export interface Exercise {
  id: string;
  title: string;
  content: string;
  difficulty: DifficultyLevel;
  knowledgeNodeIds: string[];
  solution?: string;
  hints?: string[];
  createdAt: string;
  updatedAt: string;
}

// 学习会话
export interface LearningSession {
  id: string;
  studentId: string;
  title: string;
  status: 'active' | 'completed' | 'paused';
  startedAt: string;
  endedAt?: string;
  messageCount: number;
}

// 会话消息
export interface SessionMessage {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
  attachments?: string[];
  knowledge?: import('@/modules/session/knowledge').SessionKnowledge | null;
}

// 错题记录（新版本 - 与后端 API 匹配）
export interface MistakeExercise {
  id: string;
  title: string;
  content: string;
  difficulty: number;
  knowledgePoints: string[];
}

export interface MistakeAttempt {
  studentAnswer: string;
  correctAnswer: string;
  isCorrect: boolean;
  score: number;
  submittedAt: string | null;
  timeSpentSeconds: number;
}

export interface MistakeDiagnosis {
  errorType: string | null;
  errorSubtype: string;
  severity: string;
  explanation: string;
  suggestion: string;
  relatedConcepts: string[];
}

export interface MistakeMastery {
  current: number;
  previous: number;
  trend: 'improving' | 'declining' | 'stable';
}

export interface MistakeRecord {
  id: string;
  exercise: MistakeExercise;
  attempt: MistakeAttempt;
  diagnosis: MistakeDiagnosis;
  mastery: MistakeMastery;
  errorCount: number;
  lastReviewedAt: string | null;
}

// 诊断报告
export interface DiagnosisReport {
  id: string;
  studentId: string;
  exerciseId: string;
  errorType: string;
  errorDescription: string;
  suggestions: string[];
  relatedConcepts: string[];
  createdAt: string;
}

// 学习路径
export interface LearningPath {
  id: string;
  studentId: string;
  name: string;
  description?: string;
  nodes: LearningPathNode[];
  progress: number;
  createdAt: string;
  updatedAt: string;
}

// 学习路径节点
export interface LearningPathNode {
  id: string;
  knowledgeNodeId: string;
  knowledgeNode?: KnowledgeNode;
  order: number;
  status: 'locked' | 'available' | 'in_progress' | 'completed';
  progress: number;
}

// 课程
export interface Course {
  id: string;
  name: string;
  description?: string;
  teacherId: string;
  teacher?: Teacher;
  coverImage?: string;
  startDate?: string;
  endDate?: string;
  studentCount: number;
}

// 班级
export interface Class {
  id: string;
  name: string;
  teacherId: string;
  teacher?: Teacher;
  studentIds: string[];
  students?: Student[];
  courseId?: string;
  course?: Course;
  createdAt: string;
}
