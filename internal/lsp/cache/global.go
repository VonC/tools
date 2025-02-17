package cache

import (
	"sync"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/source"
)

// GlobalCache global package cache for project
type GlobalCache interface {
	source.ICache
	Add(pkg *packages.Package)
	Put(pkg *pkg)
}

type globalPackage struct {
	pkg *pkg
}

type path2Package map[string]*globalPackage

type globalCache struct {
	mu      sync.RWMutex
	pathMap path2Package
}

// NewCache new a package cache
func NewCache() *globalCache {
	return &globalCache{pathMap: path2Package{}}
}

// Put put package into global cache
func (c *globalCache) Put(pkg *pkg) {
	c.mu.Lock()
	c.put(pkg)
	c.mu.Unlock()
}

func (c *globalCache) put(pkg *pkg) {
	pkgPath := pkg.GetTypes().Path()
	p := &globalPackage{pkg: pkg}
	c.pathMap[pkgPath] = p
}

// Get get package by package import path from global cache
func (c *globalCache) Get(pkgPath string) *pkg {
	c.mu.RLock()
	p := c.get(pkgPath)
	c.mu.RUnlock()
	return p
}

// Get get package by package import path from global cache
func (c *globalCache) get(pkgPath string) *pkg {
	p := c.pathMap[pkgPath]
	if p == nil {
		return nil
	}

	return p.pkg
}

func (c *globalCache) getGlobalPackage(pkgPath string) *globalPackage {
	c.mu.RLock()
	p := c.pathMap[pkgPath]
	c.mu.RUnlock()
	if p == nil {
		return nil
	}

	return p
}

// Walk walk the global package cache
func (c *globalCache) Walk(walkFunc source.WalkFunc) {
	c.walk(walkFunc)
}

func (c *globalCache) walk(walkFunc source.WalkFunc) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, pkg := range c.pathMap {
		if walkFunc(pkg.pkg) {
			return
		}
	}
}

func (c *globalCache) Add(pkg *packages.Package) {
	c.recursiveAdd(pkg, nil)
}

func (c *globalCache) recursiveAdd(pkg *packages.Package, parent *pkg) {
	if p := c.getGlobalPackage(pkg.PkgPath); p != nil {
		if parent != nil {
			parent.addImport(p.pkg)
		}
		return
	}

	p := newPackage(pkg)

	for _, ip := range pkg.Imports {
		c.recursiveAdd(ip, p)
	}

	c.put(p)

	if parent != nil {
		parent.addImport(p)
	}
}

// newPackage new package
func newPackage(p *packages.Package) *pkg {
	return &pkg{
		id:        packageID(p.ID),
		pkgPath:   packagePath(p.PkgPath),
		files:     createAstFiles(p),
		errors:    p.Errors,
		types:     p.Types,
		typesInfo: p.TypesInfo,
		imports:   make(map[packagePath]*pkg),
	}
}

func createAstFiles(p *packages.Package) []*astFile {
	var astFiles []*astFile
	for _, file := range p.Syntax {
		astFiles = append(astFiles, &astFile{file: file})
	}

	return astFiles
}

// addImport add import package
func (p *pkg) addImport(ip *pkg) {
	p.imports[p.pkgPath] = ip
}
