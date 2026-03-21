# Project Title - SyncWave – Collaborative Real-Time Document Editing Backend

**Team member -**
Achindra Sharma (2547105)

## Project Description
SyncWave is a backend-driven collaborative document editing engine developed using the Go programming language. The purpose of this project is to provide a robust, centralized system where users can connect, create documents, and apply real-time text operations securely. 

The system is designed to simulate a real-world concurrent editing environment in a simplified CLI-based manner. It handles text synchronization utilizing operation logs, user authentication via secure hashing, and automatic background state backups. The project prominently focuses on scalable backend logic, cryptographic data verification, and rigorous concurrency control rather than complex user interface design.

SyncWave heavily employs Go’s core features, starting from structs and slices to track operation histories, applying interfaces for polymorphism, and utilizing JSON for robust document exporting and importing offline. 

The project strictly demonstrates concurrency data safety during simultaneous modifications. By integrating goroutines, communication channels, read-write mutexes, and wait groups, SyncWave processes massive simulated bot operations securely without structural race conditions.

Overall, SyncWave serves as a comprehensive backend architecture demonstrating the practical real-world application of Go programming concepts, including synchronization, pointer state mutation, external cryptographic libraries, and automated testing suites within a single integrated project.

## Problem Statement
In collaborative environments, multiple users regularly need to edit a shared document simultaneously. Managing real-time text operations without a structured synchronization engine natively leads to severe data inconsistency, stale state overwrites, and complex race conditions.

Existing simplistic storage methods fail to handle concurrent operational modifications effectively. As the number of simultaneous live editors increases, managing incoming operation queues, maintaining secure user authentications, and logging an accurate history becomes increasingly unstable.

There is a critically high need for a centralized, concurrency-safe backend system that can properly serialize network operations, seamlessly execute modifications, and retain a reliable persistent log of all edits. The system must support efficient background processing alongside offline data preservation.

This project aims to develop SyncWave, a streamlined collaborative document backend using the Go programming language, designed exclusively to address these live-synchronization and systemic concurrency challenges.

## Objectives of the Project
The main objective of the SyncWave project is to design and develop a robust backend engine for collaborative document editing strictly using the Go programming language. The project aims to demonstrate the practical efficiency of core Go programming concepts when placed in a real-world, high-concurrency scenario.

The specific objectives of the project are:
1. To design a centralized backend logic loop for securely managing users, documents, and real-time operational editing logs.
2. To implement structured data grouping dynamically using Go types such as embedded structs, slices, and mapping registries.
3. To serialize operational history and backup document states inherently utilizing robust JSON marshaling/unmarshaling structures.
5. To extensively apply Go’s concurrency features such as goroutines, operational channels, `RWMutexes`, and wait groups tracking high-load concurrent bot editing reliably.
6. To deploy proper error handling logic, system panic recovery hooks, and detailed time-stamped operation logging.
7. To perform structured Unit testing and Load-Testing to evaluate concurrent operation correctness and WebSocket connection robustness.
8. To actively utilize pointers to accurately enact cross-system in-memory state mutations.

## System Architecture
The SyncWave system follows a segmented layered architecture that cleanly separates command ingestion, native application routing logic, and active memory processing operations. 

The architecture consists of three core layers:

### 1. Presentation Layer (CLI Interface)
The presentation layer is responsible for gathering text operations and system commands natively from users. In this project, it is implemented as an interactive console-based menu system holding infinite routing loops. This layer handles the ingestion of user configuration, transmits requests securely to the backend `Server` structure, and formats output strings displaying actualized document states.

### 2. Application Layer (Go Backend)
The application layer behaves as the strict transactional core of the system and is written purely in Go. It manages the fundamental business logic for accepting concurrent text operations, logging timestamps, routing macro execution closures, and fulfilling algorithmic authorization sequences.
This layer efficiently routes:
* Concurrent background channel ingestion
* Graceful `panic` recovery and multi-return error states
* Persistent CRDT tracking and conflict resolution
* Structured Data serialization rules (`JSON`)

### 3. Data Layer (In-Memory & Serialization Storage)
The data layer maintains the active synchronization integrity across the entire runtime. SyncWave utilizes dynamically expanding Maps and deeply structured Slices relying heavily strictly on memory pointers to ensure instantaneous server responses. Scheduled Goroutine background tickers routinely snapshot internal Document references without blocking. Authorized complete document histories can sequentially be exported/imported locally utilizing robust JSON data representations.

## Mapping of Project with Go Programming Syllabus

The SyncWave project is intentionally designed encompassing every core standard covered in the Go programming syllabus. Each technical module maps natively to systemic features described thoroughly below.

### Unit 1: Programming Fundamentals
| Syllabus Topics | Project Implementation |
| :--- | :--- |
| **Importance of Go** | Developing a high-performance backend syncing engine |
| **Variables, values, types** | Assigning `Operation` values, custom string runes |
| **Packages** | Utilizing `os`, `bufio`, `time` formatting extensively |
| **Short declaration, var** | Quick menu initialization vs explicit struct tracking |
| **Zero values** | Bootstrapping default validation logic for new user/docs |
| **fmt package** | Routing complex real-time terminal formatted displays |
| **Control flow** | Interacting with CLI infinite routing switches |
| **Loops & conditionals** | Iterating natively over text runes during append loops |

### Unit 2: Grouping Data
| Syllabus Topics | Project Implementation |
| :--- | :--- |
| **Arrays & Slices** | Tracking scalable string content and `OperationHistory` slices |
| **Slice operations** | Iteratively appending edits and utilizing copy routines |
| **Maps** | High-efficiency `O(1)` routing maps for `Clients` & `Documents` |
| **Structs** | Constructing primary `Server`, `Client`, and `Operation` entities |
| **Embedded structs** | Combining `sync.RWMutex` natively into `Document` declarations |

### Unit 3: Functions and Error Handling
| Syllabus Topics | Project Implementation |
| :--- | :--- |
| **Functions** | Containing algorithmic Document logic processing modularly |
| **Variadic functions** | Constructing dynamic format inputs handling `Log()` |
| **Anonymous functions** | Defining anonymous goroutines and `defer` hook evaluations |
| **Closures & callbacks** | Defining the dynamic `RECORD MACRO` callback closures globally |
| **Interfaces & polymorphism** | Implementing `Identifiable` guaranteeing standard output forms |
| **Defer, panic, recover** | Hard-testing structural faults preventing critical application crashing |
| **Error handling & logging** | Outputting standard dual-return errors mitigating duplicate logic |

### Unit 4: Pointers and Application
| Syllabus Topics | Project Implementation |
| :--- | :--- |
| **Pointers** | Relying on `*Server` architectures mutating underlying states natively |
| **Passing by value vs pointer** | Demonstrating mutation failures vs explicit Pointer corrections |
| **Method sets** | Defining structurally bound functional execution blocks |
| **JSON marshal/unmarshal** | Migrating complex slices dynamically into Protocol buffers over WebSockets |
| **Application** | Employing standard `encoding/json` deploying payload serialization |
| **Testing** | Building standard Table-Driven Unit testing answering strict CRDT validations |
| **Benchmarking (Load Test)** | Providing a concurrent multi-client WebSocket stress test verifying channel scaling |

### Unit 5: Concurrency
| Syllabus Topics | Project Implementation |
| :--- | :--- |
| **Concurrency vs parallelism** | Intercepting incoming inputs concurrently bypassing execution blocks |
| **Goroutines** | Detaching background server monitoring ticker functions dynamically |
| **Wait groups** | Verifying exactly 100 heavily active bot ingestions complete correctly |
| **Race conditions** | Applying explicit protection blocking Map overwrite execution collisions |
| **Mutex** | Layering atomic `sync.RWMutex` protecting read/write memory safety |
| **Channels & select** | Passing structs securely via `OpChan` implementing clean `Timeout` selects |
