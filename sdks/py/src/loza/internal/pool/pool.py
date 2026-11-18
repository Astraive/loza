from queue import Empty, SimpleQueue


class ObjectPool:
    def __init__(self, factory):
        self.factory = factory
        self.items = SimpleQueue()

    def get(self):
        try:
            return self.items.get_nowait()
        except Empty:
            return self.factory()

    def put(self, item):
        self.items.put(item)
